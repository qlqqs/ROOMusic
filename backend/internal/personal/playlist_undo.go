package personal

import (
	"context"
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type UndoRef struct {
	Token           string `json:"token"`
	RemovalRevision int64  `json:"removal_revision"`
}
type RemoveResult struct {
	Playlist PlaylistState `json:"playlist"`
	Removed  ItemRef       `json:"removed"`
	Undo     UndoRef       `json:"undo"`
}
type RestoreResult struct {
	Playlist  PlaylistState `json:"playlist"`
	Outcome   string        `json:"outcome"`
	Item      ItemRef       `json:"item"`
	Placement string        `json:"placement"`
}

func removeItem(ctx context.Context, tx *sql.Tx, user string, p PlaylistState, id string) (RemoveResult, error) {
	item, err := loadItem(ctx, tx, user, p.ID, id)
	if err != nil {
		return RemoveResult{}, err
	}
	ref, err := itemPosition(ctx, tx, user, p.ID, item)
	if err != nil {
		return RemoveResult{}, err
	}
	previous, err := neighbor(ctx, tx, user, p.ID, id, item.OrderKey, false)
	if err != nil {
		return RemoveResult{}, err
	}
	var previousTrack any
	if previous != nil {
		previousTrack = previous.TrackID
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND id=$3::uuid`, user, p.ID, id); err != nil {
		return RemoveResult{}, err
	}
	p, err = bumpPlaylist(ctx, tx, user, p, -1)
	if err != nil {
		return RemoveResult{}, err
	}
	token := uuid.New()
	_, err = tx.ExecContext(ctx, `INSERT INTO personal_playlist_undo(user_id,playlist_id,token,item_id,track_id,order_key,added_at,original_position,previous_track_id,removal_revision)
 VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9::uuid,$10)`, user, p.ID, token, item.ID, item.TrackID, item.OrderKey, item.AddedAt, ref.Position, previousTrack, p.Revision)
	return RemoveResult{p, ref, UndoRef{token, p.Revision}}, err
}

func restoreItem(ctx context.Context, tx *sql.Tx, user, key string, p PlaylistState, token string) (RestoreResult, *Result, error) {
	var item playlistItem
	var previous, restored sql.NullString
	var revision int64
	err := tx.QueryRowContext(ctx, `SELECT item_id::text,track_id::text,order_key,added_at,original_position,previous_track_id::text,removal_revision,restore_request_key::text
 FROM personal_playlist_undo WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND token=$3::uuid`, user, p.ID, token).Scan(&item.ID, &item.TrackID, &item.OrderKey, &item.AddedAt, &item.Position, &previous, &revision, &restored)
	if errors.Is(err, sql.ErrNoRows) {
		return RestoreResult{}, nil, ErrNotFound
	}
	if err != nil {
		return RestoreResult{}, nil, err
	}
	item.AddedAt = item.AddedAt.UTC()
	if restored.Valid {
		var result Result
		err = tx.QueryRowContext(ctx, `SELECT response_status,response_body FROM personal_playlist_requests WHERE user_id=$1::uuid AND idempotency_key=$2::uuid`, user, restored.String).Scan(&result.Status, &result.Body)
		return RestoreResult{}, &result, err
	}
	existing, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND track_id=$3::uuid`, user, p.ID, item.TrackID))
	result := RestoreResult{Playlist: p, Outcome: "already_present", Placement: "unchanged"}
	if err == nil {
		result.Item, err = itemPosition(ctx, tx, user, p.ID, existing)
		if err != nil {
			return RestoreResult{}, nil, err
		}
	} else {
		if !errors.Is(err, sql.ErrNoRows) {
			return RestoreResult{}, nil, err
		}
		placement := "original"
		if p.Revision != revision {
			var before *string
			before, placement, err = restoreAnchor(ctx, tx, user, p, item, previous)
			if err != nil {
				return RestoreResult{}, nil, err
			}
			item.OrderKey, err = keyBefore(ctx, tx, user, p.ID, item.ID, before)
			if err != nil {
				return RestoreResult{}, nil, err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO personal_playlist_items(id,user_id,playlist_id,track_id,order_key,added_at) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6)`, item.ID, user, p.ID, item.TrackID, item.OrderKey, item.AddedAt)
		if err != nil {
			return RestoreResult{}, nil, err
		}
		// 有效撤销不受普通加入配额限制，但仍维护安全整数与真实条目计数。
		p, err = bumpPlaylist(ctx, tx, user, p, 1)
		if err != nil {
			return RestoreResult{}, nil, err
		}
		ref, e := itemPosition(ctx, tx, user, p.ID, item)
		if e != nil {
			return RestoreResult{}, nil, e
		}
		result = RestoreResult{p, "restored", ref, placement}
	}
	_, err = tx.ExecContext(ctx, `UPDATE personal_playlist_undo SET restore_request_key=$4::uuid WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND token=$3::uuid`, user, p.ID, token, key)
	return result, nil, err
}
func restoreAnchor(ctx context.Context, tx *sql.Tx, user string, p PlaylistState, item playlistItem, previous sql.NullString) (*string, string, error) {
	if previous.Valid {
		predecessor, err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns+` FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid AND track_id=$3::uuid`, user, p.ID, previous.String))
		if err == nil {
			next, err := neighbor(ctx, tx, user, p.ID, item.ID, predecessor.OrderKey, true)
			if err != nil {
				return nil, "", err
			}
			if next == nil {
				return nil, "after_previous", nil
			}
			return &next.ID, "after_previous", nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, "", err
		}
	}
	if item.Position > p.TrackCount+1 {
		return nil, "end", nil
	}
	if item.Position == p.TrackCount+1 {
		return nil, "original_index", nil
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id::text FROM personal_playlist_items WHERE user_id=$1::uuid AND playlist_id=$2::uuid ORDER BY order_key LIMIT 1 OFFSET $3`, user, p.ID, item.Position-1).Scan(&id)
	return &id, "original_index", err
}
