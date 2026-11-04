package personal

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 两整数 advisory lock 与 OPER、ROOT、CATP 及单 BIGINT 迁移锁隔离。
const personalKeyLock int32 = 0x504b4559   // PKEY
const personalQuotaLock int32 = 0x5051554f // PQUO

func lockPersonal(ctx context.Context, tx *sql.Tx, namespace int32, value string) error {
	sum := sha256.Sum256([]byte(value))
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1::integer,$2::integer)`, namespace, int32(binary.BigEndian.Uint32(sum[:4])))
	return err
}

// Execute 在同一事务执行个人歌单命令和保存成功收据；失败不会消费幂等键。
func (s *Service) Execute(ctx context.Context, userID, key string, c Command) (Result, error) {
	if !uuid.Valid(userID) || !uuid.Valid(key) {
		return Result{}, ErrInvalidInput
	}
	userID = strings.ToLower(userID)
	key = strings.ToLower(key)
	c, err := normalizeCommand(c)
	if err != nil {
		return Result{}, err
	}
	canonical, err := json.Marshal(struct {
		SchemaVersion int `json:"schema_version"`
		Command
	}{1, c})
	if err != nil {
		return Result{}, err
	}
	hash := sha256.Sum256(canonical)
	tx, err := s.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	if err = lockPersonal(ctx, tx, personalKeyLock, userID+":"+key); err != nil {
		return Result{}, err
	}
	var existingHash []byte
	var result Result
	err = tx.QueryRowContext(ctx, `SELECT request_hash,response_status,response_body FROM personal_playlist_requests WHERE user_id=$1::uuid AND idempotency_key=$2::uuid`, userID, key).Scan(&existingHash, &result.Status, &result.Body)
	if err == nil {
		if !bytes.Equal(existingHash, hash[:]) {
			return Result{}, ErrIdempotencyConflict
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Result{}, fmt.Errorf("read personal receipt: %w", err)
	}
	if c.Kind == "create" || c.Kind == "delete" {
		if err = lockPersonal(ctx, tx, personalQuotaLock, userID); err != nil {
			return Result{}, err
		}
	}
	var p PlaylistState
	if c.Kind != "create" {
		p, err = scanPlaylist(tx.QueryRowContext(ctx, `SELECT `+playlistColumns+` FROM personal_playlists WHERE user_id=$1::uuid AND id=$2::uuid FOR UPDATE`, userID, c.PlaylistID))
		if errors.Is(err, sql.ErrNoRows) {
			return Result{}, ErrNotFound
		}
		if err != nil {
			return Result{}, err
		}
		if c.Kind != "restore_item" && p.Revision != c.ExpectedRevision {
			return Result{}, ErrRevisionConflict
		}
	}
	result, playlistID, err := s.runCommand(ctx, tx, userID, key, c, p)
	if err != nil {
		return Result{}, err
	}
	// JSONB 的规范化结果作为首次响应，保证后续重放具有相同字节。
	err = tx.QueryRowContext(ctx, `INSERT INTO personal_playlist_requests(user_id,idempotency_key,command,playlist_id,request_hash,response_status,response_body)
 VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5,$6,$7::jsonb) RETURNING response_body`, userID, key, c.Kind, playlistID, hash[:], result.Status, []byte(result.Body)).Scan(&result.Body)
	if err != nil {
		return Result{}, fmt.Errorf("save personal receipt: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("commit personal command: %w", err)
	}
	return result, nil
}

func normalizeCommand(c Command) (Command, error) {
	switch c.Kind {
	case "create", "rename", "delete", "add_items", "remove_item", "restore_item", "move_item":
	default:
		return c, ErrInvalidInput
	}
	if c.Kind != "create" {
		if !uuid.Valid(c.PlaylistID) {
			return c, ErrInvalidInput
		}
		c.PlaylistID = strings.ToLower(c.PlaylistID)
	}
	if c.Kind != "create" && c.Kind != "restore_item" && (c.ExpectedRevision < 1 || c.ExpectedRevision > MaxRevision) {
		return c, ErrInvalidInput
	}
	if c.Kind == "create" || c.Kind == "rename" {
		var err error
		c.Name, err = NormalizeName(c.Name)
		if err != nil {
			return c, err
		}
	}
	if c.Kind == "create" || c.Kind == "add_items" {
		if len(c.TrackIDs) > TrackLimit || (c.Kind == "add_items" && len(c.TrackIDs) == 0) {
			return c, ErrInvalidInput
		}
		ids := make([]string, len(c.TrackIDs))
		for i, id := range c.TrackIDs {
			if !uuid.Valid(id) {
				return c, ErrInvalidInput
			}
			ids[i] = strings.ToLower(id)
		}
		c.TrackIDs = ids
	}
	if c.Kind == "remove_item" || c.Kind == "move_item" {
		if !uuid.Valid(c.ItemID) {
			return c, ErrInvalidInput
		}
		c.ItemID = strings.ToLower(c.ItemID)
	}
	if c.Kind == "restore_item" {
		if !uuid.Valid(c.Token) {
			return c, ErrInvalidInput
		}
		c.Token = strings.ToLower(c.Token)
	}
	if c.Kind == "move_item" {
		switch c.Action {
		case "up", "down", "first", "last":
			if c.BeforeItemID != nil {
				return c, ErrInvalidInput
			}
		case "before":
			if c.BeforeItemID != nil {
				if !uuid.Valid(*c.BeforeItemID) {
					return c, ErrInvalidInput
				}
				id := strings.ToLower(*c.BeforeItemID)
				c.BeforeItemID = &id
			}
		default:
			return c, ErrInvalidInput
		}
	}
	return c, nil
}
func response(status int, body any) (Result, error) {
	data, err := json.Marshal(body)
	return Result{status, data}, err
}
func (s *Service) runCommand(ctx context.Context, tx *sql.Tx, user, key string, c Command, p PlaylistState) (Result, string, error) {
	var body any
	status := 200
	var err error
	switch c.Kind {
	case "create":
		status = 201
		body, p, err = s.createPlaylist(ctx, tx, user, c)
	case "rename":
		body, err = renamePlaylist(ctx, tx, user, p, c.Name)
	case "delete":
		_, err = tx.ExecContext(ctx, `DELETE FROM personal_playlists WHERE user_id=$1::uuid AND id=$2::uuid`, user, p.ID)
		body = struct {
			PlaylistID string `json:"playlist_id"`
			Deleted    bool   `json:"deleted"`
		}{p.ID, true}
	case "add_items":
		body, err = s.addItems(ctx, tx, user, p, c.TrackIDs)
	case "remove_item":
		body, err = removeItem(ctx, tx, user, p, c.ItemID)
	case "restore_item":
		var replay *Result
		body, replay, err = restoreItem(ctx, tx, user, key, p, c.Token)
		if replay != nil {
			return *replay, p.ID, err
		}
	case "move_item":
		body, err = moveItem(ctx, tx, user, p, c)
	}
	if err != nil {
		return Result{}, "", err
	}
	r, err := response(status, body)
	return r, p.ID, err
}
