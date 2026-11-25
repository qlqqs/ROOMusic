package personal

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

type RatedListParams struct {
	MinRating int
	Sort      string
	Page      Page
}

type RatedTrack struct {
	Track   catalog.TrackSummary `json:"track"`
	Rating  int                  `json:"rating"`
	RatedAt time.Time            `json:"rated_at"`
}

type RatedTracksPage struct {
	Items      []RatedTrack `json:"items"`
	Pagination Pagination   `json:"pagination"`
}

type RatedAlbumItem struct {
	Album   PersonalAlbum `json:"album"`
	Rating  int           `json:"rating"`
	RatedAt time.Time     `json:"rated_at"`
}

type RatedAlbumsPage struct {
	Items      []RatedAlbumItem `json:"items"`
	Pagination Pagination       `json:"pagination"`
}

type RatedArtist struct {
	Artist    catalog.ArtistSummary `json:"artist"`
	Available bool                  `json:"available"`
	Rating    int                   `json:"rating"`
	RatedAt   time.Time             `json:"rated_at"`
}

type RatedArtistsPage struct {
	Items      []RatedArtist `json:"items"`
	Pagination Pagination    `json:"pagination"`
}

type ratingRef struct {
	id, title, artist string
	rating            int
	ratedAt           time.Time
}

func (p *RatedListParams) normalizeFilter() error {
	if p.MinRating == 0 {
		p.MinRating = 1
	}
	if p.Sort == "" {
		p.Sort = "rating"
	}
	if p.MinRating < 1 || p.MinRating > 5 || (p.Sort != "rating" && p.Sort != "recent") {
		return ErrInvalidInput
	}
	return nil
}

func (p RatedListParams) orderBy(idColumn string) string {
	if p.Sort == "recent" {
		return "rated_at DESC," + idColumn + " ASC"
	}
	return "rating DESC,rated_at DESC," + idColumn + " ASC"
}

func (s *Service) RatedTracks(ctx context.Context, userID string, p RatedListParams) (RatedTracksPage, error) {
	tx, offset, err := s.beginRatedList(ctx, &p)
	if err != nil {
		return RatedTracksPage{}, err
	}
	defer tx.Rollback()
	const source = `(SELECT track_id AS target_id,rating,rated_at FROM rating_tracks WHERE user_id=$1) AS rated`
	refs, pagination, err := readRatedPage(ctx, tx, userID, p, offset, source, false)
	if err != nil {
		return RatedTracksPage{}, err
	}
	summaries, err := orderedTrackSummaries(ctx, s.catalogFor(tx), ratingRefIDs(refs), false)
	if err != nil {
		return RatedTracksPage{}, err
	}
	items := make([]RatedTrack, len(refs))
	for i, ref := range refs {
		items[i] = RatedTrack{Track: summaries[i], Rating: ref.rating, RatedAt: ref.ratedAt}
	}
	if err := tx.Commit(); err != nil {
		return RatedTracksPage{}, fmt.Errorf("finish rated tracks: %w", err)
	}
	return RatedTracksPage{Items: items, Pagination: pagination}, nil
}

func (s *Service) RatedAlbums(ctx context.Context, userID string, p RatedListParams) (RatedAlbumsPage, error) {
	tx, offset, err := s.beginRatedList(ctx, &p)
	if err != nil {
		return RatedAlbumsPage{}, err
	}
	defer tx.Rollback()
	const source = `(SELECT group_id AS target_id,rating,rated_at,saved_title,saved_artist FROM rating_albums WHERE user_id=$1) AS rated`
	refs, pagination, err := readRatedPage(ctx, tx, userID, p, offset, source, true)
	if err != nil {
		return RatedAlbumsPage{}, err
	}
	var groups map[string]catalog.PersonalReleaseGroupSummary
	if len(refs) > 0 {
		groups, err = s.catalogFor(tx).LoadReleaseGroupSummaries(ctx, ratingRefIDs(refs))
		if err != nil {
			return RatedAlbumsPage{}, fmt.Errorf("project rated albums: %w", err)
		}
	}
	items := make([]RatedAlbumItem, 0, len(refs))
	for _, ref := range refs {
		group, ok := groups[ref.id]
		if !ok {
			return RatedAlbumsPage{}, fmt.Errorf("rated album projection missing")
		}
		album, err := personalAlbum(group, ref.title, ref.artist)
		if err != nil {
			return RatedAlbumsPage{}, err
		}
		items = append(items, RatedAlbumItem{Album: album, Rating: ref.rating, RatedAt: ref.ratedAt})
	}
	if err := tx.Commit(); err != nil {
		return RatedAlbumsPage{}, fmt.Errorf("finish rated albums: %w", err)
	}
	return RatedAlbumsPage{Items: items, Pagination: pagination}, nil
}

func (s *Service) RatedArtists(ctx context.Context, userID string, p RatedListParams) (RatedArtistsPage, error) {
	tx, offset, err := s.beginRatedList(ctx, &p)
	if err != nil {
		return RatedArtistsPage{}, err
	}
	defer tx.Rollback()
	artists := s.artistCatalogFor(tx)
	if _, err := artists.ResolveArtists(ctx, nil); err != nil {
		return RatedArtistsPage{}, artistCatalogError(err, false)
	}
	// 先选出每位艺人的最新评分，避免旧的高分让合并后的艺人通过筛选。
	const source = `(SELECT DISTINCT ON (resolved_artist_id) resolved_artist_id AS target_id,rating,rated_at
		FROM rating_artists WHERE user_id=$1 ORDER BY resolved_artist_id,rated_at DESC,artist_id) AS rated`
	refs, pagination, err := readRatedPage(ctx, tx, userID, p, offset, source, false)
	if err != nil {
		return RatedArtistsPage{}, err
	}
	var summaries map[string]catalog.ArtistSummary
	if len(refs) > 0 {
		summaries, err = artists.LoadArtistSummaries(ctx, ratingRefIDs(refs))
		if err != nil {
			return RatedArtistsPage{}, artistCatalogError(err, false)
		}
	}
	items := make([]RatedArtist, 0, len(refs))
	for _, ref := range refs {
		artist, ok := summaries[ref.id]
		if !ok || artist.ID != ref.id {
			return RatedArtistsPage{}, fmt.Errorf("rated artist projection missing or mismatched")
		}
		items = append(items, RatedArtist{Artist: artist, Available: artist.Available, Rating: ref.rating, RatedAt: ref.ratedAt})
	}
	if err := tx.Commit(); err != nil {
		return RatedArtistsPage{}, fmt.Errorf("finish rated artists: %w", err)
	}
	return RatedArtistsPage{Items: items, Pagination: pagination}, nil
}

func (s *Service) beginRatedList(ctx context.Context, p *RatedListParams) (*sql.Tx, int64, error) {
	if err := p.normalizeFilter(); err != nil {
		return nil, 0, err
	}
	offset, err := readPageOffset(p.Page, 100, 0)
	if err != nil {
		return nil, 0, err
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("begin rated list: %w", err)
	}
	return tx, offset, nil
}

func readRatedPage(ctx context.Context, tx *sql.Tx, userID string, p RatedListParams, offset int64, source string, withAlbumSnapshot bool) ([]ratingRef, Pagination, error) {
	var total int64
	from := " FROM " + source + " WHERE rating >= $2"
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*)"+from, userID, p.MinRating).Scan(&total); err != nil {
		return nil, Pagination{}, fmt.Errorf("count rated list: %w", err)
	}
	fields := "target_id,rating,rated_at"
	if withAlbumSnapshot {
		fields += ",saved_title,saved_artist"
	}
	query := "SELECT " + fields + from + " ORDER BY " + p.orderBy("target_id") + " LIMIT $3 OFFSET $4"
	refs, err := readRatingRefs(ctx, tx, query, withAlbumSnapshot, userID, p.MinRating, p.Page.Size, offset)
	return refs, p.Page.pagination(total), err
}

func readRatingRefs(ctx context.Context, tx *sql.Tx, query string, withAlbumSnapshot bool, args ...any) ([]ratingRef, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read rating references: %w", err)
	}
	defer rows.Close()
	var refs []ratingRef
	for rows.Next() {
		var ref ratingRef
		fields := []any{&ref.id, &ref.rating, &ref.ratedAt}
		if withAlbumSnapshot {
			fields = append(fields, &ref.title, &ref.artist)
		}
		if err := rows.Scan(fields...); err != nil {
			return nil, fmt.Errorf("scan rating reference: %w", err)
		}
		ref.ratedAt = ref.ratedAt.UTC()
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read rating references: %w", err)
	}
	return refs, rows.Close()
}

func ratingRefIDs(refs []ratingRef) []string {
	ids := make([]string, len(refs))
	for i, ref := range refs {
		ids[i] = ref.id
	}
	return ids
}
