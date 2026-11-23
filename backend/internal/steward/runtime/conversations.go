package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// Conversation 是会话摘要，不包含消息正文。
type Conversation struct {
	Kind              string
	ItemCounts        json.RawMessage
	LastReadAt        *time.Time
	LastRunFinishedAt *time.Time
	Unread            bool
	AutoApprovedTools []string
	// PendingItemCount 是会话列表中尚待用户处理的未过期方案项数量。
	PendingItemCount int
	// ID 是会话标识。
	ID string
	// UserID 是会话所属用户。
	UserID string
	// Title 是用户可修改的标题。
	Title string
	// Mode 是会话模式，目前支持 assistant、operator 与 steward。
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
	Summary ConversationSummary `json:"-"`
	Plans   []Plan
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

const conversationColumns = `id::text,user_id::text,title,mode,model,status,created_at,updated_at,array_to_json(auto_approved_tools),last_read_at,last_run_finished_at,
 (last_run_finished_at IS NOT NULL AND (last_read_at IS NULL OR last_run_finished_at>last_read_at)) AS unread,kind`

// CreateConversation 创建空会话，未指定模式时使用 assistant。
func (s *Store) CreateConversation(ctx context.Context, userID, title, model string, modes ...string) (Conversation, error) {
	mode := "assistant"
	if len(modes) > 0 && modes[0] != "" {
		mode = modes[0]
	}
	if mode != "assistant" && mode != "operator" && mode != "steward" {
		return Conversation{}, ErrInvalidInput
	}
	return s.createConversation(ctx, userID, title, model, mode, "chat")
}

func (s *Store) CreateCurationConversation(ctx context.Context, userID, title, model string) (Conversation, error) {
	return s.createConversation(ctx, userID, title, model, "steward", "curation")
}

func (s *Store) createConversation(ctx context.Context, userID, title, model, mode, kind string) (Conversation, error) {
	conversation, err := scanConversation(s.db.QueryRowContext(ctx, `INSERT INTO steward_conversations
 (id,user_id,title,mode,model,kind) VALUES ($1::uuid,$2::uuid,$3,$5,$4,$6)
 RETURNING `+conversationColumns, uuid.New(), userID, title, model, mode, kind))
	return conversation, storeError("新建管家会话", err)
}

// ListConversations 按更新时间倒序分页列出当前用户的会话，同一时间按 ID 升序排列。
func (s *Store) ListConversations(ctx context.Context, userID string, page ConversationPage) ([]Conversation, error) {
	return s.listConversations(ctx, userID, page, "chat")
}

func (s *Store) ListCurationConversations(ctx context.Context, userID string, page ConversationPage) ([]Conversation, error) {
	return s.listConversations(ctx, userID, page, "curation")
}

func (s *Store) listConversations(ctx context.Context, userID string, page ConversationPage, kind string) ([]Conversation, error) {
	if page.Number < 1 || page.Size < 1 || page.Size > 100 || int64(page.Number-1) > (1<<63-1)/int64(page.Size) {
		return nil, ErrInvalidInput
	}
	offset := int64(page.Number-1) * int64(page.Size)
	rows, err := s.db.QueryContext(ctx, `WITH page AS (
 SELECT `+conversationColumns+` FROM steward_conversations
 WHERE user_id=$1::uuid AND kind=$4 ORDER BY updated_at DESC,id ASC LIMIT $2 OFFSET $3
), pending AS (
 SELECT p.conversation_id,COUNT(*) AS item_count
 FROM steward_plans p JOIN steward_plan_items i ON i.plan_id=p.id
 JOIN page c ON c.id::uuid=p.conversation_id
 WHERE p.user_id=$1::uuid AND p.status='pending' AND p.expires_at>NOW()
 AND i.decision='pending' AND i.result IS NULL
 GROUP BY p.conversation_id
), curation_counts AS (
 SELECT i.conversation_id,i.status,COUNT(*) AS n FROM curation_pending_items i
 JOIN page c ON c.kind='curation' AND i.conversation_id=c.id::uuid
 GROUP BY i.conversation_id,i.status
), item_counts AS (
 SELECT conversation_id,jsonb_object_agg(status,n) AS counts FROM curation_counts GROUP BY conversation_id
)
 SELECT c.*,COALESCE(p.item_count,0),CASE WHEN c.kind='curation' THEN COALESCE(i.counts,'{}'::jsonb) END FROM page c
 LEFT JOIN item_counts i ON i.conversation_id=c.id::uuid
 LEFT JOIN pending p ON p.conversation_id=c.id::uuid
 ORDER BY c.updated_at DESC,c.id ASC`, userID, page.Size, offset, kind)
	if err != nil {
		return nil, storeError("列出管家会话", err)
	}
	defer rows.Close()
	conversations := []Conversation{}
	for rows.Next() {
		var pendingCount int
		var counts []byte
		conversation, err := scanConversation(rows, &pendingCount, &counts)
		if err != nil {
			return nil, storeError("读取管家会话摘要", err)
		}
		conversation.ItemCounts = counts
		conversation.PendingItemCount = pendingCount
		conversations = append(conversations, conversation)
	}
	if err := rows.Err(); err != nil {
		return nil, storeError("列出管家会话", err)
	}
	return conversations, storeError("关闭管家会话列表读取", rows.Close())
}

// GetConversation 在 READ COMMITTED 事务内用 FOR UPDATE 锁定会话，收尾过期方案后读取完整历史。
// 事务会写入过期状态及工具结果；不存在与属于他人都返回 ErrNotFound。
func (s *Store) GetConversation(ctx context.Context, userID, conversationID string) (ConversationDetail, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ConversationDetail{}, storeError("开始读取管家会话", err)
	}
	defer tx.Rollback()
	if _, err := lockConversation(ctx, tx, userID, conversationID); err != nil {
		return ConversationDetail{}, err
	}
	if err := s.resolvePendingPlans(ctx, tx, userID, conversationID, "expired", true); err != nil {
		return ConversationDetail{}, err
	}
	conversation, err := scanConversation(tx.QueryRowContext(ctx, `SELECT `+conversationColumns+`
 FROM steward_conversations WHERE user_id=$1::uuid AND id=$2::uuid`, userID, conversationID))
	if err != nil {
		return ConversationDetail{}, storeError("读取管家会话", err)
	}
	var summary ConversationSummary
	if err := tx.QueryRowContext(ctx, `SELECT summary,summary_through_message_id::text,summary_updated_at FROM steward_conversations WHERE user_id=$1::uuid AND id=$2::uuid`, userID, conversationID).Scan(&summary.Text, &summary.ThroughMessageID, &summary.UpdatedAt); err != nil {
		return ConversationDetail{}, storeError("读取管家历史摘要", err)
	}
	messages, err := readMessages(ctx, tx, userID, conversationID)
	if err != nil {
		return ConversationDetail{}, err
	}
	if err := readToolCalls(ctx, tx, userID, conversationID, messages); err != nil {
		return ConversationDetail{}, err
	}
	plans, err := readPlans(ctx, tx, userID, conversationID, false)
	if err != nil {
		return ConversationDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return ConversationDetail{}, storeError("完成管家会话读取", err)
	}
	for _, plan := range plans {
		if plan.Status == "pending" {
			for _, item := range plan.Items {
				if item.Decision == "pending" && len(item.Result) == 0 {
					conversation.PendingItemCount++
				}
			}
		}
	}
	return ConversationDetail{Conversation: conversation, Messages: messages, Plans: plans, Summary: summary}, nil
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
	var processing bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM curation_pending_items WHERE conversation_id=$1::uuid AND status='in_progress')`, conversationID).Scan(&processing); err != nil {
		return err
	}
	if processing {
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
	return s.releaseConversation(ctx, userID, conversationID, false)
}

// FinishConversation 仅在运行真正开始后记录结束时间，前置失败只释放占用。
func (s *Store) FinishConversation(ctx context.Context, userID, conversationID string) error {
	return s.releaseConversation(ctx, userID, conversationID, true)
}

func (s *Store) releaseConversation(ctx context.Context, userID, conversationID string, finished bool) error {
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
	if err := requireAffected(tx.ExecContext(ctx, `UPDATE steward_conversations SET status='idle',updated_at=clock_timestamp(),
 last_run_finished_at=CASE WHEN $3 THEN clock_timestamp() ELSE last_run_finished_at END
 WHERE user_id=$1::uuid AND id=$2::uuid`, userID, conversationID, finished)); err != nil {
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
	result, err := tx.ExecContext(ctx, `UPDATE steward_conversations SET status='idle',updated_at=clock_timestamp(),last_run_finished_at=clock_timestamp() WHERE status='running'`)
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
func (s *Store) UpdateConversation(ctx context.Context, userID, conversationID string, title, model *string, modes ...*string) (Conversation, error) {
	var mode *string
	if len(modes) > 0 {
		mode = modes[0]
	}
	if mode != nil && *mode != "assistant" && *mode != "operator" && *mode != "steward" {
		return Conversation{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, err
	}
	defer tx.Rollback()
	status, err := lockConversation(ctx, tx, userID, conversationID)
	if err != nil {
		return Conversation{}, err
	}
	if mode != nil {
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT kind FROM steward_conversations WHERE id=$1::uuid`, conversationID).Scan(&kind); err != nil {
			return Conversation{}, err
		}
		if kind == "curation" {
			return Conversation{}, ErrCurationConversation
		}
		if status == "running" {
			return Conversation{}, ErrConversationBusy
		}
		if err := s.resolvePendingPlans(ctx, tx, userID, conversationID, "expired", true); err != nil {
			return Conversation{}, err
		}
		var pending bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM steward_plans WHERE conversation_id=$1::uuid AND status='pending')`, conversationID).Scan(&pending); err != nil {
			return Conversation{}, err
		}
		if pending {
			return Conversation{}, ErrConversationBusy
		}
	}
	conversation, err := scanConversation(tx.QueryRowContext(ctx, `UPDATE steward_conversations SET title=COALESCE($3,title),model=COALESCE($4,model),mode=COALESCE($5,mode),updated_at=clock_timestamp() WHERE user_id=$1::uuid AND id=$2::uuid RETURNING `+conversationColumns, userID, conversationID, title, model, mode))
	if err != nil {
		return Conversation{}, storeError("修改管家会话", err)
	}
	return conversation, tx.Commit()
}

func scanConversation(row scanner, extra ...any) (Conversation, error) {
	var conversation Conversation
	var approved []byte
	fields := []any{&conversation.ID, &conversation.UserID, &conversation.Title, &conversation.Mode,
		&conversation.Model, &conversation.Status, &conversation.CreatedAt, &conversation.UpdatedAt, &approved, &conversation.LastReadAt, &conversation.LastRunFinishedAt, &conversation.Unread, &conversation.Kind}
	err := row.Scan(append(fields, extra...)...)
	if err == nil {
		err = json.Unmarshal(approved, &conversation.AutoApprovedTools)
	}
	conversation.CreatedAt = conversation.CreatedAt.UTC()
	conversation.UpdatedAt = conversation.UpdatedAt.UTC()
	return conversation, err
}

// CountConversations 返回当前用户的会话总数，供分页响应使用。
func (s *Store) CountConversations(ctx context.Context, userID string) (int64, error) {
	return s.countConversations(ctx, userID, "chat")
}

func (s *Store) CountCurationConversations(ctx context.Context, userID string) (int64, error) {
	return s.countConversations(ctx, userID, "curation")
}

func (s *Store) countConversations(ctx context.Context, userID, kind string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM steward_conversations WHERE user_id=$1::uuid AND kind=$2`, userID, kind).Scan(&count)
	return count, storeError("统计管家会话", err)
}

// MarkConversationRead 只推进本人会话的已读时间，不改变列表排序。
func (s *Store) MarkConversationRead(ctx context.Context, userID, conversationID string) error {
	return storeError("标记管家会话已读", requireAffected(s.db.ExecContext(ctx, `UPDATE steward_conversations SET last_read_at=clock_timestamp() WHERE user_id=$1::uuid AND id=$2::uuid`, userID, conversationID)))
}

// CountUnreadConversations 在数据库侧按运行完成时间统计，不逐会话读取。
func (s *Store) CountUnreadConversations(ctx context.Context, userID string) (int64, error) {
	var count int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM steward_conversations WHERE user_id=$1::uuid AND kind='chat' AND last_run_finished_at IS NOT NULL AND (last_read_at IS NULL OR last_run_finished_at>last_read_at)`, userID).Scan(&count)
	return count, storeError("统计未读管家会话", err)
}
