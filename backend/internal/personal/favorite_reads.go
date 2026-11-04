package personal

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type FavoriteTrack struct {
	Track     catalog.TrackSummary `json:"track"`
	CreatedAt time.Time            `json:"created_at"`
}
type FavoriteTracksPage struct {
	Items      []FavoriteTrack `json:"items"`
	Pagination Pagination      `json:"pagination"`
	Revision   int64           `json:"revision"`
}
type PersonalAlbum struct {
	GroupID        string                  `json:"group_id"`
	Available      bool                    `json:"available"`
	Title          string                  `json:"title"`
	Artist         string                  `json:"artist"`
	ReleaseCount   int                     `json:"release_count"`
	Representative *catalog.ReleaseSummary `json:"representative"`
}
type FavoriteAlbum struct {
	Album     PersonalAlbum `json:"album"`
	CreatedAt time.Time     `json:"created_at"`
}
type FavoriteAlbumsPage struct {
	Items      []FavoriteAlbum `json:"items"`
	Pagination Pagination      `json:"pagination"`
	Revision   int64           `json:"revision"`
}
type favoriteTrackRef struct {
	id      string
	created time.Time
}
type favoriteAlbumRef struct {
	id, title, artist string
	created           time.Time
}

func (s *Service) FavoriteTracks(ctx context.Context, userID string, page Page) (FavoriteTracksPage, error) {
	offset, err := readPageOffset(page, 100, 0)
	if err != nil {
		return FavoriteTracksPage{}, err
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return FavoriteTracksPage{}, fmt.Errorf("begin favorite tracks: %w", err)
	}
	defer tx.Rollback()
	result := FavoriteTracksPage{Items: []FavoriteTrack{}}
	result.Revision, err = favoriteRevision(ctx, tx, userID, page.Revision)
	if err != nil {
		return FavoriteTracksPage{}, err
	}
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM favorite_tracks WHERE user_id=$1::uuid`, userID).Scan(&total); err != nil {
		return FavoriteTracksPage{}, fmt.Errorf("count favorite tracks: %w", err)
	}
	refs, err := readFavoriteTrackRefs(ctx, tx, `SELECT track_id::text,created_at FROM favorite_tracks WHERE user_id=$1::uuid ORDER BY created_at DESC,favorite_tracks.track_id ASC LIMIT $2 OFFSET $3`, userID, page.Size, offset)
	if err != nil {
		return FavoriteTracksPage{}, err
	}
	ids := make([]string, len(refs))
	for i, ref := range refs {
		ids[i] = ref.id
	}
	summaries, err := orderedTrackSummaries(ctx, s.catalogFor(tx), ids, false)
	if err != nil {
		return FavoriteTracksPage{}, err
	}
	for i, ref := range refs {
		result.Items = append(result.Items, FavoriteTrack{Track: summaries[i], CreatedAt: ref.created.UTC()})
	}
	result.Pagination = page.pagination(total)
	if err := tx.Commit(); err != nil {
		return FavoriteTracksPage{}, fmt.Errorf("finish favorite tracks: %w", err)
	}
	return result, nil
}

func readFavoriteTrackRefs(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]favoriteTrackRef, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read favorite references: %w", err)
	}
	defer rows.Close()
	refs := make([]favoriteTrackRef, 0)
	for rows.Next() {
		var ref favoriteTrackRef
		if err := rows.Scan(&ref.id, &ref.created); err != nil {
			return nil, fmt.Errorf("scan favorite reference: %w", err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read favorite references: %w", err)
	}
	return refs, rows.Close()
}

func (s *Service) FavoriteAlbums(ctx context.Context, userID string, page Page) (FavoriteAlbumsPage, error) {
	offset, err := readPageOffset(page, 100, 0)
	if err != nil {
		return FavoriteAlbumsPage{}, err
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return FavoriteAlbumsPage{}, fmt.Errorf("begin favorite albums: %w", err)
	}
	defer tx.Rollback()
	result := FavoriteAlbumsPage{Items: []FavoriteAlbum{}}
	result.Revision, err = favoriteRevision(ctx, tx, userID, page.Revision)
	if err != nil {
		return FavoriteAlbumsPage{}, err
	}
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM favorite_albums WHERE user_id=$1::uuid`, userID).Scan(&total); err != nil {
		return FavoriteAlbumsPage{}, fmt.Errorf("count favorite albums: %w", err)
	}
	refs, err := readFavoriteAlbumRefs(ctx, tx, userID, page.Size, offset)
	if err != nil {
		return FavoriteAlbumsPage{}, err
	}
	ids := make([]string, len(refs))
	for i, ref := range refs {
		ids[i] = ref.id
	}
	groups := map[string]catalog.PersonalReleaseGroupSummary{}
	if len(ids) > 0 {
		groups, err = s.catalogFor(tx).LoadReleaseGroupSummaries(ctx, ids)
		if err != nil {
			return FavoriteAlbumsPage{}, fmt.Errorf("project favorite albums: %w", err)
		}
	}
	for _, ref := range refs {
		group, ok := groups[ref.id]
		if !ok {
			return FavoriteAlbumsPage{}, fmt.Errorf("favorite album projection missing")
		}
		album, err := personalAlbum(group, ref.title, ref.artist)
		if err != nil {
			return FavoriteAlbumsPage{}, err
		}
		result.Items = append(result.Items, FavoriteAlbum{Album: album, CreatedAt: ref.created.UTC()})
	}
	result.Pagination = page.pagination(total)
	if err := tx.Commit(); err != nil {
		return FavoriteAlbumsPage{}, fmt.Errorf("finish favorite albums: %w", err)
	}
	return result, nil
}

func readFavoriteAlbumRefs(ctx context.Context, tx *sql.Tx, userID string, size int, offset int64) ([]favoriteAlbumRef, error) {
	rows, err := tx.QueryContext(ctx, `SELECT group_id::text,created_at,saved_title,saved_artist FROM favorite_albums WHERE user_id=$1::uuid ORDER BY created_at DESC,favorite_albums.group_id ASC LIMIT $2 OFFSET $3`, userID, size, offset)
	if err != nil {
		return nil, fmt.Errorf("read favorite album references: %w", err)
	}
	defer rows.Close()
	refs := make([]favoriteAlbumRef, 0)
	for rows.Next() {
		var ref favoriteAlbumRef
		if err := rows.Scan(&ref.id, &ref.created, &ref.title, &ref.artist); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return refs, rows.Close()
}
