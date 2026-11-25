package personal

import (
	"context"
	"database/sql"
	"time"
)

type TrackRating struct {
	TrackID string `json:"track_id"`
	Rating  *int   `json:"rating"`
}

type AlbumRating struct {
	GroupID string `json:"group_id"`
	Rating  *int   `json:"rating"`
}

type ArtistRating struct {
	ArtistID         string  `json:"artist_id"`
	ResolvedArtistID *string `json:"resolved_artist_id"`
	Rating           *int    `json:"rating"`
}

type RatingLookup struct {
	Tracks  []TrackRating  `json:"tracks"`
	Albums  []AlbumRating  `json:"albums"`
	Artists []ArtistRating `json:"artists"`
}

type RatedAlbum struct {
	GroupID string    `json:"group_id"`
	Rating  int       `json:"rating"`
	RatedAt time.Time `json:"rated_at"`
}

func (s *Service) LookupRatings(ctx context.Context, user string, trackIDs, groupIDs, artistIDs []string) (RatingLookup, error) {
	tracks, err := normalizeIDs(trackIDs, TrackLimit)
	if err != nil {
		return RatingLookup{}, err
	}
	groups, err := normalizeIDs(groupIDs, 1000)
	if err != nil {
		return RatingLookup{}, err
	}
	artists, err := normalizeIDs(artistIDs, 1000)
	if err != nil {
		return RatingLookup{}, err
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return RatingLookup{}, err
	}
	defer tx.Rollback()

	result := RatingLookup{Tracks: []TrackRating{}, Albums: []AlbumRating{}, Artists: []ArtistRating{}}
	trackRatings, err := readRatingValues(ctx, tx, `SELECT track_id,rating FROM rating_tracks WHERE user_id=$1 AND track_id=ANY($2::uuid[])`, user, tracks)
	if err != nil {
		return RatingLookup{}, err
	}
	for _, id := range tracks {
		result.Tracks = append(result.Tracks, TrackRating{id, trackRatings[id]})
	}
	albumRatings, err := readRatingValues(ctx, tx, `SELECT group_id,rating FROM rating_albums WHERE user_id=$1 AND group_id=ANY($2::uuid[])`, user, groups)
	if err != nil {
		return RatingLookup{}, err
	}
	for _, id := range groups {
		result.Albums = append(result.Albums, AlbumRating{id, albumRatings[id]})
	}
	if len(artists) > 0 {
		resolved, err := s.artistCatalogFor(tx).ResolveArtists(ctx, artists)
		if err != nil {
			return RatingLookup{}, artistCatalogError(err, false)
		}
		targets := make([]string, 0, len(resolved))
		for _, target := range resolved {
			targets = append(targets, target)
		}
		artistRatings, err := readRatingValues(ctx, tx, `SELECT DISTINCT ON (resolved_artist_id) resolved_artist_id,rating
			FROM rating_artists WHERE user_id=$1 AND resolved_artist_id=ANY($2::uuid[])
			ORDER BY resolved_artist_id,rated_at DESC,artist_id`, user, targets)
		if err != nil {
			return RatingLookup{}, err
		}
		for _, id := range artists {
			status := ArtistRating{ArtistID: id}
			if target, ok := resolved[id]; ok {
				status.ResolvedArtistID, status.Rating = &target, artistRatings[target]
			}
			result.Artists = append(result.Artists, status)
		}
	}
	if err := tx.Commit(); err != nil {
		return RatingLookup{}, err
	}
	return result, nil
}

func readRatingValues(ctx context.Context, tx *sql.Tx, query, user string, ids []string) (map[string]*int, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, query, user, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make(map[string]*int, len(ids))
	for rows.Next() {
		var id string
		var rating int
		if err := rows.Scan(&id, &rating); err != nil {
			return nil, err
		}
		values[id] = &rating
	}
	return values, rows.Err()
}

func (s *Service) HighestRatedAlbums(ctx context.Context, user string, offset, size int) ([]RatedAlbum, error) {
	if offset < 0 || size < 0 || size > 500 {
		return nil, ErrInvalidInput
	}
	rows, err := s.database.QueryContext(ctx, `SELECT group_id,rating,rated_at FROM rating_albums
		WHERE user_id=$1 ORDER BY rating DESC,rated_at DESC,group_id LIMIT $2 OFFSET $3`, user, size, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]RatedAlbum, 0, size)
	for rows.Next() {
		var item RatedAlbum
		if err := rows.Scan(&item.GroupID, &item.Rating, &item.RatedAt); err != nil {
			return nil, err
		}
		item.RatedAt = item.RatedAt.UTC()
		result = append(result, item)
	}
	return result, rows.Err()
}
