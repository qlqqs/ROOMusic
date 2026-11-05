package maintenance

import (
	"context"
	"database/sql"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/artistgraph"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/writegate"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

// 艺人状态行复用 projection_version 列；本版本包含署名、主名、候选与首字母规则。
const artistProjectionVersion = artistgraph.Version

type artistState struct{ searchindex.State }

func loadArtistState(ctx context.Context, q searchindex.Queryer) (artistState, error) {
	var s artistState
	err := q.QueryRowContext(ctx, `SELECT target_generation,published_generation,textnorm_version,search_projection_version,
 published_textnorm_version,published_search_projection_version,catalog_epoch,build_id::text,status,manifest_sealed,live_updated,started_at
 FROM catalog_projection_state WHERE capability='artists'`).Scan(
		&s.TargetGeneration, &s.PublishedGeneration, &s.TextnormVersion, &s.SearchProjectionVersion,
		&s.PublishedTextnormVersion, &s.PublishedSearchProjectionVersion, &s.CatalogEpoch, &s.BuildID, &s.Status, &s.ManifestSealed, &s.LiveUpdated, &s.StartedAt)
	return s, err
}

func (s artistState) CurrentVersion() bool {
	return s.TextnormVersion == int64(textnorm.Version) && s.SearchProjectionVersion == artistProjectionVersion
}

func (s artistState) Ready() bool {
	return s.Status == "ready" && s.ManifestSealed && s.TargetGeneration > 0 && s.TargetGeneration == s.PublishedGeneration &&
		s.TextnormVersion == s.PublishedTextnormVersion && s.SearchProjectionVersion == s.PublishedSearchProjectionVersion && s.CurrentVersion()
}

func initializeArtists(ctx context.Context, tx *sql.Tx, preflightErr error) error {
	state, err := loadArtistState(ctx, tx)
	if err != nil {
		return err
	}
	if !state.CurrentVersion() {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_state SET target_generation=target_generation+1,
 textnorm_version=$1,search_projection_version=$2,build_id=gen_random_uuid(),status='preparing',
 catalog_epoch=catalog_epoch+1,manifest_sealed=FALSE,live_updated=0,started_at=NOW(),updated_at=NOW() WHERE capability='artists'`, textnorm.Version, artistProjectionVersion)
		if err != nil {
			return err
		}
	}
	if preflightErr != nil {
		_, err = tx.ExecContext(ctx, `UPDATE catalog_projection_state SET status='failed',updated_at=NOW() WHERE capability='artists'`)
	}
	return err
}

func lockArtistBuild(ctx context.Context, tx *sql.Tx, expected artistState) error {
	if err := writegate.Lock(ctx, tx); err != nil {
		return err
	}
	state, err := loadArtistState(ctx, tx)
	if err != nil {
		return err
	}
	if state.BuildID != expected.BuildID || state.TargetGeneration != expected.TargetGeneration || !state.CurrentVersion() {
		return errBuildChanged
	}
	return nil
}

func markArtistsFailed(ctx context.Context, conn *sql.Conn, state artistState) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer tx.Rollback()
	if err = lockArtistBuild(ctx, tx, state); err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `UPDATE catalog_projection_state SET status='failed',updated_at=NOW()
 WHERE capability='artists' AND build_id=$1::uuid AND status<>'ready'`, state.BuildID); err == nil {
		_ = tx.Commit()
	}
}
