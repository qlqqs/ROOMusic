package personal

import (
	"context"
	"database/sql"
	"fmt"
)

type PlaylistQueue struct {
	Playlist PlaylistState `json:"playlist"`
	QueueTracks
}

type playlistTrackRef struct {
	playlistID, trackID string
	orderKey            int64
}

// readPlaylistTrackBatch 关闭本批 rows 后，调用方才能在同一事务读取 catalog。
func readPlaylistTrackBatch(ctx context.Context, tx *sql.Tx, userID string, playlistIDs []string, cursor *playlistTrackRef) ([]playlistTrackRef, error) {
	query := `SELECT playlist_id::text,track_id::text,order_key FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=ANY($2::text[]::uuid[])`
	args := []any{userID, playlistIDs}
	if cursor != nil {
		query += ` AND (playlist_id,order_key)>($3::uuid,$4::bigint)`
		args = append(args, cursor.playlistID, cursor.orderKey)
	}
	query += ` ORDER BY personal_playlist_items.playlist_id ASC,order_key ASC LIMIT 10000`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read playlist references: %w", err)
	}
	defer rows.Close()
	refs := make([]playlistTrackRef, 0)
	for rows.Next() {
		var ref playlistTrackRef
		if err := rows.Scan(&ref.playlistID, &ref.trackID, &ref.orderKey); err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return refs, rows.Close()
}

func (s *Service) PlaylistQueue(ctx context.Context, userID, playlistID string) (PlaylistQueue, error) {
	ids, err := normalizeIDs([]string{playlistID}, 1)
	if err != nil {
		return PlaylistQueue{}, err
	}
	tx, err := s.readTx(ctx)
	if err != nil {
		return PlaylistQueue{}, fmt.Errorf("begin playlist queue: %w", err)
	}
	defer tx.Rollback()
	state, err := ownedPlaylistState(ctx, tx, userID, ids[0])
	if err != nil {
		return PlaylistQueue{}, err
	}
	source := s.catalogFor(tx)
	selection := queueSelection{ids: make([]string, 0, TrackLimit)}
	var cursor *playlistTrackRef
	for {
		refs, err := readPlaylistTrackBatch(ctx, tx, userID, ids, cursor)
		if err != nil {
			return PlaylistQueue{}, err
		}
		trackIDs := make([]string, len(refs))
		for i, ref := range refs {
			trackIDs[i] = ref.trackID
		}
		if err := selection.add(ctx, source, trackIDs); err != nil {
			return PlaylistQueue{}, err
		}
		if len(refs) < TrackLimit {
			break
		}
		last := refs[len(refs)-1]
		cursor = &last
	}
	if selection.total != state.TrackCount {
		return PlaylistQueue{}, fmt.Errorf("playlist queue track count inconsistent")
	}
	queue, err := selection.project(ctx, source)
	if err != nil {
		return PlaylistQueue{}, err
	}
	if err := tx.Commit(); err != nil {
		return PlaylistQueue{}, fmt.Errorf("finish playlist queue: %w", err)
	}
	return PlaylistQueue{Playlist: state, QueueTracks: queue}, nil
}
