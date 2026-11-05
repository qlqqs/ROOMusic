package personal

import (
	"context"
	"fmt"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type FavoriteArtist struct {
	Artist    catalog.ArtistSummary `json:"artist"`
	CreatedAt time.Time             `json:"created_at"`
	Available bool                  `json:"available"`
}

type FavoriteArtistsPage struct {
	Items      []FavoriteArtist `json:"items"`
	Pagination Pagination       `json:"pagination"`
	Revision   int64            `json:"revision"`
}

func (s *Service) FavoriteArtists(ctx context.Context, userID string, page Page) (FavoriteArtistsPage, error) {
	offset, err := readPageOffset(page, 100, 0)
	if err != nil {
		return FavoriteArtistsPage{}, err
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return FavoriteArtistsPage{}, fmt.Errorf("begin favorite artists: %w", err)
	}
	defer tx.Rollback()
	source := s.artistCatalogFor(tx)
	// 即使收藏为空，也不能将整理中的目录伪装成空列表。
	if _, err := source.ResolveArtists(ctx, nil); err != nil {
		return FavoriteArtistsPage{}, artistCatalogError(err, false)
	}
	result := FavoriteArtistsPage{Items: []FavoriteArtist{}}
	result.Revision, err = favoriteRevision(ctx, tx, userID, page.Revision)
	if err != nil {
		return FavoriteArtistsPage{}, err
	}
	var total int64
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(DISTINCT resolved_artist_id) FROM artist_favorites WHERE user_id=$1::uuid`, userID).Scan(&total); err != nil {
		return FavoriteArtistsPage{}, fmt.Errorf("count favorite artists: %w", err)
	}
	refs, err := readFavoriteTrackRefs(ctx, tx, `SELECT resolved_artist_id::text,MAX(created_at) AS created_at FROM artist_favorites WHERE user_id=$1::uuid GROUP BY resolved_artist_id ORDER BY created_at DESC,artist_favorites.resolved_artist_id ASC LIMIT $2 OFFSET $3`, userID, page.Size, offset)
	if err != nil {
		return FavoriteArtistsPage{}, err
	}
	ids := make([]string, len(refs))
	for i, ref := range refs {
		ids[i] = ref.id
	}
	summaries := map[string]catalog.ArtistSummary{}
	if len(ids) > 0 {
		summaries, err = source.LoadArtistSummaries(ctx, ids)
		if err != nil {
			return FavoriteArtistsPage{}, artistCatalogError(err, false)
		}
	}
	for _, ref := range refs {
		summary, ok := summaries[ref.id]
		if !ok || summary.ID != ref.id {
			return FavoriteArtistsPage{}, fmt.Errorf("favorite artist projection missing")
		}
		result.Items = append(result.Items, FavoriteArtist{Artist: summary, CreatedAt: ref.created.UTC(), Available: summary.Available})
	}
	result.Pagination = page.pagination(total)
	if err := tx.Commit(); err != nil {
		return FavoriteArtistsPage{}, fmt.Errorf("finish favorite artists: %w", err)
	}
	return result, nil
}
