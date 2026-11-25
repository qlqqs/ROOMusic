package personal

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// PlaylistTracks 读取本人歌单及其完整有序曲目 ID，供整单替换使用。
func (s *Service) PlaylistTracks(ctx context.Context, userID, playlistID string) (PlaylistState, []string, error) {
	if !uuid.Valid(playlistID) {
		return PlaylistState{}, nil, ErrInvalidInput
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return PlaylistState{}, nil, err
	}
	defer tx.Rollback()
	state, err := ownedPlaylistState(ctx, tx, userID, playlistID)
	if err != nil {
		return PlaylistState{}, nil, err
	}
	ids, err := readPlaylistTrackIDs(ctx, tx, userID, state.ID)
	if err != nil {
		return PlaylistState{}, nil, err
	}
	return state, ids, tx.Commit()
}

func readPlaylistTrackIDs(ctx context.Context, tx *sql.Tx, user, playlist string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT track_id::text FROM personal_playlist_items
 WHERE user_id=$1::uuid AND playlist_id=$2::uuid ORDER BY order_key`, user, playlist)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Service) replacePlaylistItems(ctx context.Context, tx *sql.Tx, user string, p PlaylistState, c Command) (RenameResult, error) {
	current, err := readPlaylistTrackIDs(ctx, tx, user, p.ID)
	if err != nil {
		return RenameResult{}, err
	}
	name := c.Name
	if name == "" {
		name = p.Name
	}
	if slices.Equal(current, c.TrackIDs) {
		return renamePlaylist(ctx, tx, user, p, name)
	}
	if p.Revision >= MaxRevision {
		return RenameResult{}, fmt.Errorf("personal revision exhausted")
	}
	old := make(map[string]bool, len(current))
	for _, id := range current {
		old[id] = true
	}
	var added, itemIDs []string
	var keys []int64
	for i, id := range c.TrackIDs {
		if !old[id] {
			added = append(added, id)
			itemIDs = append(itemIDs, uuid.New())
			keys = append(keys, int64(i+1)*orderStep)
		}
	}
	if len(c.TrackIDs) > TrackLimit && len(added) > 0 {
		return RenameResult{}, ErrTrackLimit
	}
	if err := s.requireTracks(ctx, tx, added); err != nil {
		return RenameResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM personal_playlist_items
 WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND NOT(track_id=ANY($3::text[]::uuid[]))`, user, p.ID, c.TrackIDs); err != nil {
		return RenameResult{}, err
	}
	// 保留仍在歌单中的条目身份，允许旧顺序和新顺序的键在事务内短暂重合。
	if _, err := tx.ExecContext(ctx, `SET CONSTRAINTS personal_playlist_items_order_uq DEFERRED`); err != nil {
		return RenameResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `WITH ordered AS MATERIALIZED (
 SELECT i.id,x.position*$4::bigint AS order_key
 FROM personal_playlist_items i JOIN unnest($3::text[]::uuid[]) WITH ORDINALITY AS x(track_id,position) USING (track_id)
 WHERE i.user_id=$1::uuid AND i.playlist_id=$2::uuid
)
 UPDATE personal_playlist_items i SET order_key=o.order_key FROM ordered o WHERE i.id=o.id AND i.order_key<>o.order_key`, user, p.ID, c.TrackIDs, orderStep); err != nil {
		return RenameResult{}, err
	}
	if len(added) > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO personal_playlist_items(id,user_id,playlist_id,track_id,order_key)
 SELECT x.id,$1::uuid,$2::uuid,x.track_id,x.order_key FROM unnest($3::text[]::uuid[],$4::text[]::uuid[],$5::bigint[]) AS x(id,track_id,order_key)`, user, p.ID, itemIDs, added, keys); err != nil {
			return RenameResult{}, err
		}
	}
	p, err = scanPlaylist(tx.QueryRowContext(ctx, `UPDATE personal_playlists SET name=$3,track_count=$4,revision=revision+1,updated_at=clock_timestamp()
 WHERE user_id=$1::uuid AND id=$2::uuid RETURNING `+playlistColumns, user, p.ID, name, len(c.TrackIDs)))
	if err == nil {
		err = checkTrackCount(ctx, tx, user, p)
	}
	return RenameResult{p, true}, err
}
