package maintenance

import (
	"context"
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
)

func publishArtistsOrRepair(ctx context.Context, conn *sql.Conn, state artistState, options RunOptions) (bool, error) {
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err = lockArtistBuild(ctx, tx, state); err != nil {
		return false, err
	}
	search, err := searchindex.LoadState(ctx, tx)
	if err != nil {
		return false, err
	}
	if !search.Ready() || search.TextnormVersion != state.TextnormVersion {
		return false, errBuildChanged
	}
	var pending bool
	if err = tx.QueryRowContext(ctx, `SELECT NOT manifest_sealed OR NOT EXISTS(
 SELECT 1 FROM catalog_projection_phases WHERE build_id=$1::uuid AND phase='artist_profiles' AND subject_kind='artist') OR EXISTS(
 SELECT 1 FROM catalog_projection_work WHERE build_id=$1::uuid AND NOT completed)
 FROM catalog_projection_state WHERE capability='artists'`, state.BuildID).Scan(&pending); err != nil {
		return false, err
	}
	if pending {
		return false, nil
	}
	var gap bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM releases r WHERE `+artistGapPredicate+`)
 OR EXISTS(SELECT 1 FROM artists a WHERE `+artistProfileGapPredicate+`)`, state.TargetGeneration).Scan(&gap); err != nil {
		return false, err
	}
	if gap {
		if err = tx.Rollback(); err != nil {
			return false, err
		}
		return false, discoverArtists(ctx, conn, state, true)
	}
	if options.RefreshArtistResolutions == nil {
		return false, ErrArtistResolutionRefreshRequired
	}
	mappings, err := artistgraph.Mappings(ctx, tx)
	if err != nil {
		return false, err
	}
	if mappings == nil {
		return false, errors.New("artist resolution mapping is absent")
	}
	if err = options.RefreshArtistResolutions(ctx, tx, mappings); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_state SET published_generation=target_generation,
 published_textnorm_version=textnorm_version,published_search_projection_version=search_projection_version,
 status='ready',catalog_epoch=catalog_epoch+1,updated_at=NOW() WHERE capability='artists'`)
	if err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
