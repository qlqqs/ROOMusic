package personal

import (
	"context"
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

// SetFavorites 校验三类稳定身份后原子切换收藏。实际变化只递增一次集合修订号。
func (s *Service) SetFavorites(ctx context.Context, userID string, trackIDs, groupIDs, artistIDs []string, favorited bool) error {
	tracks, err := normalizeIDs(trackIDs, TrackLimit)
	if err != nil {
		return ErrNotFound
	}
	groups, err := normalizeIDs(groupIDs, 1000)
	if err != nil {
		return ErrNotFound
	}
	artists, err := normalizeIDs(artistIDs, 1000)
	if err != nil {
		return ErrNotFound
	}
	tx, err := s.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var canonical []string
	if len(artists) > 0 {
		if err := catalog.AcquireArtistWriteGate(ctx, tx); err != nil {
			return err
		}
		resolved, err := s.artistCatalogFor(tx).ResolveArtists(ctx, artists)
		if err != nil {
			return artistCatalogError(err, true)
		}
		canonical = make([]string, 0, len(artists))
		for _, id := range artists {
			target, ok := resolved[id]
			if !ok {
				return ErrNotFound
			}
			canonical = append(canonical, target)
		}
	}
	revision, err := lockFavoriteState(ctx, tx, userID)
	if err != nil {
		return err
	}
	if err := s.requireTracks(ctx, tx, tracks); err != nil {
		return err
	}
	titles, names := make([]string, len(groups)), make([]string, len(groups))
	if len(groups) > 0 {
		summaries, err := s.catalogFor(tx).LoadReleaseGroupSummaries(ctx, groups)
		if err != nil {
			return err
		}
		for i, id := range groups {
			group, ok := summaries[id]
			if !ok {
				return ErrNotFound
			}
			if group.Representative != nil {
				titles[i], names[i] = group.Representative.Title, group.Representative.Artist
			}
		}
	}
	batches := []struct {
		ids      []string
		relation favoriteRelation
		insert   string
		extra    []any
	}{
		{tracks, favoriteRelation{"favorite_tracks", "track_id"}, `INSERT INTO favorite_tracks(user_id,track_id)
 SELECT $1::uuid,id FROM unnest($2::text[]::uuid[]) AS x(id) ON CONFLICT DO NOTHING`, nil},
		{groups, favoriteRelation{"favorite_albums", "group_id"}, `INSERT INTO favorite_albums(user_id,group_id,saved_title,saved_artist)
 SELECT $1::uuid,id,title,artist FROM unnest($2::text[]::uuid[],$3::text[],$4::text[]) AS x(id,title,artist) ON CONFLICT DO NOTHING`, []any{titles, names}},
		{canonical, favoriteRelation{"artist_favorites", "resolved_artist_id"}, `INSERT INTO artist_favorites(user_id,artist_id,resolved_artist_id)
 SELECT $1::uuid,anchor,target FROM (
 SELECT DISTINCT ON (target) target,anchor
 FROM unnest($2::text[]::uuid[],$3::text[]::uuid[]) WITH ORDINALITY AS x(target,anchor,position)
 ORDER BY target,position
 ) AS targets
 WHERE NOT EXISTS (SELECT 1 FROM artist_favorites WHERE user_id=$1::uuid AND resolved_artist_id=targets.target)`, []any{artists}},
	}
	var changed int64
	for _, batch := range batches {
		if len(batch.ids) == 0 {
			continue
		}
		args := []any{userID, batch.ids}
		query := `DELETE FROM ` + batch.relation.table + ` WHERE user_id=$1::uuid AND ` + batch.relation.column + `=ANY($2::text[]::uuid[])`
		if favorited {
			query, args = batch.insert, append(args, batch.extra...)
		}
		result, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		changed += count
	}
	if changed > 0 {
		if revision >= MaxRevision {
			return fmt.Errorf("favorite revision exhausted")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE personal_favorite_state SET revision=revision+1 WHERE user_id=$1::uuid`, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}
