// Package memory 管理按调用者隔离的管家记忆。
package memory

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const Limit = 50
const MaxLength = 500

var ErrInvalidInput = errors.New("记忆内容无效")
var ErrLimitReached = errors.New("记忆数量已达上限")
var ErrNotFound = errors.New("记忆不存在")

// Item 不包含所属用户字段；所有查询必须显式传入已认证用户。
type Item struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type List struct {
	Items     []Item `json:"items"`
	Limit     int    `json:"limit"`
	MaxLength int    `json:"max_length"`
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) List(ctx context.Context, userID string) (List, error) {
	result := List{Items: []Item{}, Limit: Limit, MaxLength: MaxLength}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,content,created_at,updated_at FROM steward_memories WHERE user_id=$1::uuid ORDER BY updated_at DESC,id ASC`, userID)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.Content, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return result, err
		}
		item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	return result, rows.Close()
}

// Remember 的 nil ID 表示新增；指定 ID 只更新本人已有记录。
func (s *Store) Remember(ctx context.Context, userID string, id *string, content string) (Item, error) {
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) || strings.TrimSpace(content) == "" || utf8.RuneCountInString(content) > MaxLength {
		return Item{}, ErrInvalidInput
	}
	if id != nil && !uuid.Valid(*id) {
		return Item{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
	// 同一用户的创建串行计数；锁是事务级的，独立于记忆是否已有记录。
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 1057))`, userID); err != nil {
		return Item{}, err
	}
	var row *sql.Row
	if id == nil {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM steward_memories WHERE user_id=$1::uuid`, userID).Scan(&count); err != nil {
			return Item{}, err
		}
		if count >= Limit {
			return Item{}, ErrLimitReached
		}
		row = tx.QueryRowContext(ctx, `INSERT INTO steward_memories (id,user_id,content,created_at,updated_at) SELECT $1::uuid,$2::uuid,$3,ts,ts FROM (SELECT clock_timestamp() AS ts) AS creation_time RETURNING id::text,content,created_at,updated_at`, uuid.New(), userID, content)
	} else {
		row = tx.QueryRowContext(ctx, `UPDATE steward_memories SET content=$3,updated_at=clock_timestamp() WHERE user_id=$1::uuid AND id=$2::uuid RETURNING id::text,content,created_at,updated_at`, userID, *id, content)
	}
	var item Item
	if err := row.Scan(&item.ID, &item.Content, &item.CreatedAt, &item.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Item{}, ErrNotFound
		}
		return Item{}, err
	}
	item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
	return item, tx.Commit()
}

// Forget 的 nil ID 表示清除本人全部记忆。
func (s *Store) Forget(ctx context.Context, userID string, id *string) error {
	if id != nil && !uuid.Valid(*id) {
		return ErrInvalidInput
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM steward_memories WHERE user_id=$1::uuid AND ($2::uuid IS NULL OR id=$2::uuid)`, userID, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && id != nil && count == 0 {
		return ErrNotFound
	}
	return err
}
