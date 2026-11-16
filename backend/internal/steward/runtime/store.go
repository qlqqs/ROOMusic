// Package runtime 拥有管家按用户隔离的设置、会话、消息与工具调用存储。
package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrNotFound 表示资源不存在或不属于当前用户，不区分这两种情况。
var ErrNotFound = errors.New("管家资源不存在")

// ErrConversationBusy 表示会话已有运行占用。
var ErrConversationBusy = errors.New("管家会话忙")

// ErrInvalidInput 表示分页参数或工具调用结束状态不合法。
var ErrInvalidInput = errors.New("管家存储参数无效")

// Store 使用调用方提供的数据库连接池保存管家数据，不负责连接或迁移。
// 所有 userID 必须来自入口取得的当前用户，不能采用请求正文中的用户标识。
type Store struct {
	db *sql.DB
}

// NewStore 构造存储，不执行数据库操作。
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

type scanner interface {
	Scan(...any) error
}

func storeError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func requireAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrNotFound
	}
	return nil
}

func lockConversation(ctx context.Context, tx *sql.Tx, userID, conversationID string) (string, error) {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM steward_conversations
 WHERE user_id=$1::uuid AND id=$2::uuid FOR UPDATE`, userID, conversationID).Scan(&status)
	return status, storeError("锁定管家会话", err)
}

func touchConversation(ctx context.Context, tx *sql.Tx, userID, conversationID string) error {
	return requireAffected(tx.ExecContext(ctx, `UPDATE steward_conversations SET updated_at=clock_timestamp()
 WHERE user_id=$1::uuid AND id=$2::uuid`, userID, conversationID))
}
