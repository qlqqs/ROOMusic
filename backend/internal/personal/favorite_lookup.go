package personal

import (
	"context"
	"database/sql"
	"fmt"
)

type FavoriteTrackStatus struct {
	TrackID   string `json:"track_id"`
	Favorited bool   `json:"favorited"`
}
type FavoriteAlbumStatus struct {
	GroupID   string `json:"group_id"`
	Favorited bool   `json:"favorited"`
}
type FavoriteArtistStatus struct {
	ArtistID         string  `json:"artist_id"`
	ResolvedArtistID *string `json:"resolved_artist_id"`
	Favorited        bool    `json:"favorited"`
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
				status.Favorited = found[target]
			}
			result.Artists = append(result.Artists, status)
		}
	}
	result.Revision, err = favoriteRevision(ctx, tx, userID, nil)
	if err != nil {
		return FavoriteLookup{}, err
	}
	trackSet, err := lookupFavoriteIDs(ctx, tx, userID, tracks, favoriteRelation{"favorite_tracks", "track_id"})
	if err != nil {
		return FavoriteLookup{}, err
	}
	groupSet, err := lookupFavoriteIDs(ctx, tx, userID, groups, favoriteRelation{"favorite_albums", "group_id"})
	if err != nil {
		return FavoriteLookup{}, err
	}
	for _, id := range tracks {
		result.Tracks = append(result.Tracks, FavoriteTrackStatus{TrackID: id, Favorited: trackSet[id]})
	}
	for _, id := range groups {
		result.Albums = append(result.Albums, FavoriteAlbumStatus{GroupID: id, Favorited: groupSet[id]})
	}
	if err := tx.Commit(); err != nil {
		return FavoriteLookup{}, fmt.Errorf("finish favorite lookup: %w", err)
	}
	return result, nil
}

func lookupFavoriteIDs(ctx context.Context, tx *sql.Tx, userID string, ids []string, relation favoriteRelation) (map[string]bool, error) {
	found := make(map[string]bool, len(ids))
	if len(ids) == 0 {
		return found, nil
	}
	projection := relation.column + "::text"
	// 艺人多个原锚点可解析到同一身份，批量状态只返回有界的不同身份。
	if relation.table == "artist_favorites" {
		projection = "DISTINCT " + projection
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+projection+` FROM `+relation.table+` WHERE user_id=$1::uuid AND `+relation.column+`=ANY($2::text[]::uuid[])`, userID, ids)
	if err != nil {
		return nil, fmt.Errorf("lookup favorite relationships: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		found[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return found, rows.Close()
}
