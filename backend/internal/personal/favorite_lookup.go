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
type FavoriteLookup struct {
	Revision int64                 `json:"revision"`
	Tracks   []FavoriteTrackStatus `json:"tracks"`
	Albums   []FavoriteAlbumStatus `json:"albums"`
}

func (s *Service) LookupFavorites(ctx context.Context, userID string, trackIDs, groupIDs []string) (FavoriteLookup, error) {
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
	result := FavoriteLookup{Tracks: []FavoriteTrackStatus{}, Albums: []FavoriteAlbumStatus{}}
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
	rows, err := tx.QueryContext(ctx, `SELECT `+relation.column+`::text FROM `+relation.table+` WHERE user_id=$1::uuid AND `+relation.column+`=ANY($2::text[]::uuid[])`, userID, ids)
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
