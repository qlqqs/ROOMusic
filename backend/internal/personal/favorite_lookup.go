package personal

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type FavoriteTrackStatus struct {
	TrackID   string     `json:"track_id"`
	Favorited bool       `json:"favorited"`
	CreatedAt *time.Time `json:"-"`
}
type FavoriteAlbumStatus struct {
	GroupID   string     `json:"group_id"`
	Favorited bool       `json:"favorited"`
	CreatedAt *time.Time `json:"-"`
}
type FavoriteArtistStatus struct {
	ArtistID         string     `json:"artist_id"`
	ResolvedArtistID *string    `json:"resolved_artist_id"`
	Favorited        bool       `json:"favorited"`
	CreatedAt        *time.Time `json:"-"`
}
type FavoriteLookup struct {
	Revision int64                  `json:"revision"`
	Tracks   []FavoriteTrackStatus  `json:"tracks"`
	Albums   []FavoriteAlbumStatus  `json:"albums"`
	Artists  []FavoriteArtistStatus `json:"artists"`
}

// LookupFavorites 的艺人参数仅允许省略或一组；空数组不读取艺人目录，响应 artists 仍为 []。
func (s *Service) LookupFavorites(ctx context.Context, userID string, trackIDs, groupIDs []string, artistIDs ...[]string) (FavoriteLookup, error) {
	if len(artistIDs) > 1 {
		return FavoriteLookup{}, ErrInvalidInput
	}
	var artists []string
	if len(artistIDs) == 1 {
		var err error
		artists, err = normalizeIDs(artistIDs[0], 1000)
		if err != nil {
			return FavoriteLookup{}, err
		}
	}
	tracks, err := normalizeIDs(trackIDs, TrackLimit)
	if err != nil {
		return FavoriteLookup{}, err
	}
	groups, err := normalizeIDs(groupIDs, 1000)
	if err != nil {
		return FavoriteLookup{}, err
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return FavoriteLookup{}, fmt.Errorf("begin favorite lookup: %w", err)
	}
	defer tx.Rollback()
	result, err := s.lookupFavorites(ctx, tx, userID, tracks, groups, artists)
	if err != nil {
		return FavoriteLookup{}, err
	}
	if err := tx.Commit(); err != nil {
		return FavoriteLookup{}, fmt.Errorf("finish favorite lookup: %w", err)
	}
	return result, nil
}

// LookupBrowseState 在同一个只读快照内读取浏览实体的评分和收藏。
func (s *Service) LookupBrowseState(ctx context.Context, userID string, trackIDs, groupIDs, artistIDs []string) (RatingLookup, FavoriteLookup, error) {
	tracks, err := normalizeIDs(trackIDs, TrackLimit)
	if err != nil {
		return RatingLookup{}, FavoriteLookup{}, err
	}
	groups, err := normalizeIDs(groupIDs, 1000)
	if err != nil {
		return RatingLookup{}, FavoriteLookup{}, err
	}
	artists, err := normalizeIDs(artistIDs, 1000)
	if err != nil {
		return RatingLookup{}, FavoriteLookup{}, err
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return RatingLookup{}, FavoriteLookup{}, err
	}
	defer tx.Rollback()
	ratings, err := s.lookupRatings(ctx, tx, userID, tracks, groups, artists)
	if err != nil {
		return RatingLookup{}, FavoriteLookup{}, err
	}
	favorites, err := s.lookupFavorites(ctx, tx, userID, tracks, groups, artists)
	if err != nil {
		return RatingLookup{}, FavoriteLookup{}, err
	}
	return ratings, favorites, tx.Commit()
}

func (s *Service) lookupFavorites(ctx context.Context, tx *sql.Tx, userID string, tracks, groups, artists []string) (FavoriteLookup, error) {
	result := FavoriteLookup{Tracks: []FavoriteTrackStatus{}, Albums: []FavoriteAlbumStatus{}, Artists: []FavoriteArtistStatus{}}
	if len(artists) > 0 {
		resolved, err := s.artistCatalogFor(tx).ResolveArtists(ctx, artists)
		if err != nil {
			return FavoriteLookup{}, artistCatalogError(err, false)
		}
		canonical := make([]string, 0, len(resolved))
		for _, id := range artists {
			if target, ok := resolved[id]; ok {
				canonical = append(canonical, target)
			}
		}
		found, err := lookupFavoriteIDs(ctx, tx, userID, canonical, favoriteRelation{"artist_favorites", "resolved_artist_id"})
		if err != nil {
			return FavoriteLookup{}, err
		}
		for _, id := range artists {
			status := FavoriteArtistStatus{ArtistID: id}
			if target, ok := resolved[id]; ok {
				status.ResolvedArtistID = &target
				status.CreatedAt = found[target]
				status.Favorited = status.CreatedAt != nil
			}
			result.Artists = append(result.Artists, status)
		}
	}
	revision, err := favoriteRevision(ctx, tx, userID, nil)
	if err != nil {
		return FavoriteLookup{}, err
	}
	result.Revision = revision
	trackSet, err := lookupFavoriteIDs(ctx, tx, userID, tracks, favoriteRelation{"favorite_tracks", "track_id"})
	if err != nil {
		return FavoriteLookup{}, err
	}
	groupSet, err := lookupFavoriteIDs(ctx, tx, userID, groups, favoriteRelation{"favorite_albums", "group_id"})
	if err != nil {
		return FavoriteLookup{}, err
	}
	for _, id := range tracks {
		result.Tracks = append(result.Tracks, FavoriteTrackStatus{TrackID: id, Favorited: trackSet[id] != nil, CreatedAt: trackSet[id]})
	}
	for _, id := range groups {
		result.Albums = append(result.Albums, FavoriteAlbumStatus{GroupID: id, Favorited: groupSet[id] != nil, CreatedAt: groupSet[id]})
	}
	return result, nil
}

func lookupFavoriteIDs(ctx context.Context, tx *sql.Tx, userID string, ids []string, relation favoriteRelation) (map[string]*time.Time, error) {
	found := make(map[string]*time.Time, len(ids))
	if len(ids) == 0 {
		return found, nil
	}
	// 艺人的等价锚点沿用收藏列表的最新时间。
	rows, err := tx.QueryContext(ctx, `SELECT `+relation.column+`::text,MAX(created_at) FROM `+relation.table+` WHERE user_id=$1::uuid AND `+relation.column+`=ANY($2::text[]::uuid[]) GROUP BY `+relation.column, userID, ids)
	if err != nil {
		return nil, fmt.Errorf("lookup favorite relationships: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var created time.Time
		if err := rows.Scan(&id, &created); err != nil {
			return nil, err
		}
		stamp := created.UTC()
		found[id] = &stamp
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return found, rows.Close()
}
