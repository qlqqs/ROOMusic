package runtime

import (
	"context"
	"database/sql"
)

// RunStore 是一次运行需要的会话与消息存储接口，Store 满足此接口。
type RunStore interface {
	AcquireConversation(context.Context, string, string) error
	ReleaseConversation(context.Context, string, string) error
	GetConversation(context.Context, string, string) (ConversationDetail, error)
	RenameConversation(context.Context, string, string, string) error
	AppendMessage(context.Context, string, string, MessageInput) (Message, error)
	FinishToolCallMessage(context.Context, string, string, string, ToolCallResult, MessageInput) (Message, error)
}

// FinishToolCallMessage 原子地结束工具调用并追加对应工具消息，避免重放时缺少结果。
func (s *Store) FinishToolCallMessage(ctx context.Context, userID, conversationID, callID string, result ToolCallResult, input MessageInput) (Message, error) {
	if (result.Status != "succeeded" && result.Status != "failed") || input.Role != "tool" || len(input.ToolCalls) != 0 {
		return Message{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Message{}, storeError("开始保存管家工具消息", err)
	}
	defer tx.Rollback()
	if _, err := lockConversation(ctx, tx, userID, conversationID); err != nil {
		return Message{}, err
	}
	if err := finishToolCall(ctx, tx, userID, conversationID, callID, result); err != nil {
		return Message{}, err
	}
	message, err := appendMessage(ctx, tx, userID, conversationID, input)
	if err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, storeError("提交管家工具消息", err)
	}
	return message, nil
}

// RunCompletionStore 为持久化未读状态的存储区分已开始运行与前置失败。
type RunCompletionStore interface {
	FinishConversation(context.Context, string, string) error
}
