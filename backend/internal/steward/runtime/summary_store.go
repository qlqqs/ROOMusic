package runtime

import (
	"context"
	"time"
)

// ConversationSummary 仅供模型重放，HTTP 只投影 ThroughMessageID。
type ConversationSummary struct {
	Text             string
	ThroughMessageID *string
	UpdatedAt        *time.Time
}

// SummaryStore 将摘要与回合分界一起保存；不改动原始历史。
type SummaryStore interface {
	SaveSummary(context.Context, string, string, string, string) error
}

func (s *Store) SaveSummary(ctx context.Context, userID, conversationID, text, throughID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return storeError("开始保存管家摘要", err)
	}
	defer tx.Rollback()
	if _, err := lockConversation(ctx, tx, userID, conversationID); err != nil {
		return err
	}
	// 分界必须属于本会话，下一条必须是用户消息，避免拆开工具调用和结果。
	err = requireAffected(tx.ExecContext(ctx, `UPDATE steward_conversations c SET summary=$3,summary_through_message_id=$4::uuid,summary_updated_at=clock_timestamp()
 WHERE c.user_id=$1::uuid AND c.id=$2::uuid AND EXISTS (
 SELECT 1 FROM steward_messages m JOIN steward_messages n ON n.conversation_id=m.conversation_id AND n.sequence=m.sequence+1
 WHERE m.conversation_id=c.id AND m.id=$4::uuid AND n.role='user')`, userID, conversationID, text, throughID))
	if err != nil {
		return storeError("保存管家摘要", err)
	}
	return storeError("提交管家摘要", tx.Commit())
}
