package runtime

import (
	"context"
	"database/sql"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// Conversation 是会话摘要，不包含消息正文。
type Conversation struct {
	// ID 是会话标识。
	ID string
	// UserID 是会话所属用户。
	UserID string
	// Title 是用户可修改的标题。
	Title string
	// Mode 是会话模式，当前只创建 assistant 会话。
	Mode string
	// Model 是该会话使用的模型。
	Model string
	// Status 是 idle 或 running。
	Status string
	// CreatedAt 是创建时间。
	CreatedAt time.Time
	// UpdatedAt 是最后修改时间。
	UpdatedAt time.Time
}

// ConversationDetail 包含同一快照下的会话、消息与工具调用。
type ConversationDetail struct {
	// Conversation 是会话摘要。
	Conversation Conversation
	// Messages 按会话内序号升序排列，每条消息包含其工具调用。
	Messages []Message
}

// ConversationPage 是从一开始的会话分页参数，每页最多一百条。
type ConversationPage struct {
	// Number 是页码，从一开始。
	Number int
	// Size 是每页条数，范围为一至一百。
	Size int
}

const conversationColumns = `id::text,user_id::text,title,mode,model,status,created_at,updated_at`

// CreateConversation 为当前用户新建 assistant 模式的空会话。
func (s *Store) CreateConversation(ctx context.Context, userID, title, model string) (Conversation, error) {
	conversation, err := scanConversation(s.db.QueryRowContext(ctx, `INSERT INTO steward_conversations
 (id,user_id,title,mode,model) VALUES ($1::uuid,$2::uuid,$3,'assistant',$4)
 RETURNING `+conversationColumns, uuid.New(), userID, title, model))
	return conversation, storeError("新建管家会话", err)
}

// ListConversations 按更新时间倒序分页列出当前用户的会话，同一时间按 ID 升序排列。
func (s *Store) ListConversations(ctx context.Context, userID string, page ConversationPage) ([]Conversation, error) {
	if page.Number < 1 || page.Size < 1 || page.Size > 100 || int64(page.Number-1) > (1<<63-1)/int64(page.Size) {
		return nil, ErrInvalidInput
	}
	offset := int64(page.Number-1) * int64(page.Size)
	rows, err := s.db.QueryContext(ctx, `SELECT `+conversationColumns+` FROM steward_conversations
 WHERE user_id=$1::uuid ORDER BY updated_at DESC,id ASC LIMIT $2 OFFSET $3`, userID, page.Size, offset)
	if err != nil {
		return nil, storeError("列出管家会话", err)
	}
	defer rows.Close()
	conversations := []Conversation{}
	for rows.Next() {
		conversation, err := scanConversation(rows)
		if err != nil {
			return nil, storeError("读取管家会话摘要", err)
		}
		conversations = append(conversations, conversation)
	}
	if err := rows.Err(); err != nil {
		return nil, storeError("列出管家会话", err)
	}
	return conversations, storeError("关闭管家会话列表读取", rows.Close())
}

// GetConversation 读取当前用户的完整会话，不存在与属于他人都返回 ErrNotFound。
func (s *Store) GetConversation(ctx context.Context, userID, conversationID string) (ConversationDetail, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return ConversationDetail{}, storeError("开始读取管家会话", err)
	}
	defer tx.Rollback()
	conversation, err := scanConversation(tx.QueryRowContext(ctx, `SELECT `+conversationColumns+`
 FROM steward_conversations WHERE user_id=$1::uuid AND id=$2::uuid`, userID, conversationID))
	if err != nil {
		return ConversationDetail{}, storeError("读取管家会话", err)
	}
	messages, err := readMessages(ctx, tx, userID, conversationID)
	if err != nil {
		return ConversationDetail{}, err
	}
	if err := readToolCalls(ctx, tx, userID, conversationID, messages); err != nil {
		return ConversationDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConversationDetail{}, storeError("完成管家会话读取", err)
	}
	return ConversationDetail{Conversation: conversation, Messages: messages}, nil
}

// DeleteConversation 删除当前用户的空闲会话，数据库级联删除消息与工具调用；运行中返回 ErrConversationBusy。
func (s *Store) DeleteConversation(ctx context.Context, userID, conversationID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storeError("开始删除管家会话", err)
	}
	defer tx.Rollback()
	status, err := lockConversation(ctx, tx, userID, conversationID)
	if err != nil {
		return err
	}
	if status == "running" {
		return ErrConversationBusy
	}
	err = requireAffected(tx.ExecContext(ctx, `DELETE FROM steward_conversations
 WHERE user_id=$1::uuid AND id=$2::uuid`, userID, conversationID))
	if err != nil {
		return storeError("删除管家会话", err)
	}
	return storeError("提交管家会话删除", tx.Commit())
}

// RenameConversation 修改当前用户的会话标题并更新排序时间。
func (s *Store) RenameConversation(ctx context.Context, userID, conversationID, title string) error {
	err := requireAffected(s.db.ExecContext(ctx, `UPDATE steward_conversations SET title=$3,updated_at=clock_timestamp()
 WHERE user_id=$1::uuid AND id=$2::uuid`, userID, conversationID, title))
	return storeError("修改管家会话标题", err)
}

// AcquireConversation 原子地将当前用户的会话从 idle 置为 running，已占用时返回 ErrConversationBusy。
func (s *Store) AcquireConversation(ctx context.Context, userID, conversationID string) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return storeError("开始占用管家会话", err)
	}
	defer tx.Rollback()
	status, err := lockConversation(ctx, tx, userID, conversationID)
	if err != nil {
		return err
	}
	if status == "running" {
		return ErrConversationBusy
	}
	err = requireAffected(tx.ExecContext(ctx, `UPDATE steward_conversations SET status='running',updated_at=clock_timestamp()
 WHERE user_id=$1::uuid AND id=$2::uuid AND status='idle'`, userID, conversationID))
	if err != nil {
		return storeError("占用管家会话", err)
	}
	return storeError("完成管家会话占用", tx.Commit())
}

// ReleaseConversation 将当前用户的会话置回 idle，已空闲的会话同样成功。
func (s *Store) ReleaseConversation(ctx context.Context, userID, conversationID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storeError("开始释放管家会话", err)
	}
	defer tx.Rollback()
	if _, err := lockConversation(ctx, tx, userID, conversationID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE steward_tool_calls t SET status='failed',error_code='interrupted'
 FROM steward_messages m JOIN steward_conversations c ON c.id=m.conversation_id
 WHERE t.message_id=m.id AND c.user_id=$1::uuid AND c.id=$2::uuid AND t.status='running'`, userID, conversationID); err != nil {
		return storeError("结束未完成的管家工具调用", err)
	}
	if err := requireAffected(tx.ExecContext(ctx, `UPDATE steward_conversations SET status='idle',updated_at=clock_timestamp()
 WHERE user_id=$1::uuid AND id=$2::uuid`, userID, conversationID)); err != nil {
		return storeError("释放管家会话", err)
	}
	return storeError("提交管家会话释放", tx.Commit())
}

// ResetRunningConversations 仅供进程启动使用，将遗留工具调用标为中断并释放会话。
func (s *Store) ResetRunningConversations(ctx context.Context) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, storeError("开始恢复管家会话", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE steward_tool_calls SET status='failed',error_code='interrupted' WHERE status='running'`); err != nil {
		return 0, storeError("恢复管家工具调用", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE steward_conversations SET status='idle',updated_at=clock_timestamp() WHERE status='running'`)
	if err != nil {
		return 0, storeError("恢复管家会话状态", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, storeError("读取管家会话恢复数量", err)
	}
	return count, storeError("提交管家会话恢复", tx.Commit())
}

// UpdateConversation 原子修改当前用户会话的标题和模型，省略字段保留原值。
func (s *Store) UpdateConversation(ctx context.Context, userID, conversationID string, title, model *string) (Conversation, error) {
	conversation, err := scanConversation(s.db.QueryRowContext(ctx, `UPDATE steward_conversations
 SET title=COALESCE($3,title),model=COALESCE($4,model),updated_at=clock_timestamp()
 WHERE user_id=$1::uuid AND id=$2::uuid RETURNING `+conversationColumns, userID, conversationID, title, model))
	return conversation, storeError("修改管家会话", err)
}

func scanConversation(row scanner) (Conversation, error) {
	var conversation Conversation
	err := row.Scan(&conversation.ID, &conversation.UserID, &conversation.Title, &conversation.Mode,
		&conversation.Model, &conversation.Status, &conversation.CreatedAt, &conversation.UpdatedAt)
	conversation.CreatedAt = conversation.CreatedAt.UTC()
	conversation.UpdatedAt = conversation.UpdatedAt.UTC()
	return conversation, err
}

// CountConversations 返回当前用户的会话总数，供分页响应使用。
func (s *Store) CountConversations(ctx context.Context, userID string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM steward_conversations WHERE user_id=$1::uuid`, userID).Scan(&count)
	return count, storeError("统计管家会话", err)
}
