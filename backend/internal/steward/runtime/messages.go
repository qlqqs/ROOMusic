package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// Message 是完整落库的消息，内容不在存储层解析或改写。
type Message struct {
	// ID 是消息标识。
	ID string
	// ConversationID 是所属会话。
	ConversationID string
	// Sequence 是会话内从一开始的递增序号。
	Sequence int64
	// Role 是 user、assistant 或 tool。
	Role string
	// Content 原样保存适配器给出的 JSON，包含不透明块及协议内工具调用标识。
	// 调用方负责只将不透明块回传原协议，且不向界面暴露这些块。
	Content json.RawMessage
	// InputTokens 为 nil 表示输入用量未知，不估算。
	InputTokens *int64
	// OutputTokens 为 nil 表示输出用量未知，不估算。
	OutputTokens *int64
	// CreatedAt 是落库时间。
	CreatedAt time.Time
	// ToolCalls 是本条消息关联的工具调用，按消息内调用位置排序。
	ToolCalls []ToolCall
}

// MessageInput 是一次原子追加的消息及其工具调用。
type MessageInput struct {
	// Role 是 user、assistant 或 tool。
	Role string
	// Content 是完整 JSON，存储层不解释内容结构。
	Content json.RawMessage
	// InputTokens 为 nil 表示输入用量未知。
	InputTokens *int64
	// OutputTokens 为 nil 表示输出用量未知。
	OutputTokens *int64
	// ToolCalls 随消息在同一事务中以 running 状态创建。
	ToolCalls []ToolCallInput
}

// ToolCall 是一次工具执行的持久化记录。
type ToolCall struct {
	// ID 是存储标识，与 Provider 的协议内调用标识独立。
	ID string
	// MessageID 是请求此调用的消息标识。
	MessageID string
	// Position 是消息内从零开始的调用位置。
	Position int
	// Name 是注册的工具名。
	Name string
	// Input 是工具输入的完整 JSON。
	Input json.RawMessage
	// Output 为 nil 时表示尚无输出。
	Output json.RawMessage
	// Truncated 表示输出已被工具调用入口截断。
	Truncated bool
	// Untrusted 表示工具输出包含不可信文本。
	Untrusted bool
	// Status 是 running、succeeded 或 failed。
	Status string
	// ErrorCode 是失败时的稳定错误码，不保存原始错误信息。
	ErrorCode *string
	// DurationMS 是执行耗时毫秒数，未知时为 nil。
	DurationMS *int64
}

// ToolCallInput 是待执行工具的输入，存储层生成独立的调用标识。
type ToolCallInput struct {
	// Name 是注册的工具名。
	Name string
	// Input 是工具输入的完整 JSON。
	Input json.RawMessage
	// Untrusted 来自工具定义，标明其输出是否含不可信文本。
	Untrusted bool
}

// ToolCallResult 是已结束工具调用的结果。
type ToolCallResult struct {
	// Output 是输出 JSON；没有输出时为 nil。
	Output json.RawMessage
	// Truncated 表示输出已被截断。
	Truncated bool
	// Status 只能为 succeeded 或 failed。
	Status string
	// ErrorCode 是稳定错误码，没有错误时为 nil。
	ErrorCode *string
	// DurationMS 是耗时毫秒数，未知时为 nil。
	DurationMS *int64
}

// AppendMessage 锁定当前用户的会话后分配序号，将消息与 running 工具调用一起提交。
// 返回的 ToolCalls 沿用输入顺序，供调用方把协议内调用标识与存储 ID 对应起来。
func (s *Store) AppendMessage(ctx context.Context, userID, conversationID string, input MessageInput) (Message, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return Message{}, storeError("开始追加管家消息", err)
	}
	defer tx.Rollback()
	if _, err := lockConversation(ctx, tx, userID, conversationID); err != nil {
		return Message{}, err
	}
	message, err := appendMessage(ctx, tx, userID, conversationID, input)
	if err != nil {
		return Message{}, err
	}
	if err := tx.Commit(); err != nil {
		return Message{}, storeError("提交管家消息", err)
	}
	return message, nil
}

func appendMessage(ctx context.Context, tx *sql.Tx, userID, conversationID string, input MessageInput) (Message, error) {
	// 锁行与分配序号分成两条语句，使 READ COMMITTED 能看到前一个持锁事务的消息。
	message, err := scanMessage(tx.QueryRowContext(ctx, `INSERT INTO steward_messages
 (id,conversation_id,sequence,role,content,input_tokens,output_tokens,created_at)
 SELECT $3::uuid,c.id,COALESCE(MAX(m.sequence),0)+1,$4,$5::json,$6,$7,clock_timestamp()
 FROM steward_conversations c LEFT JOIN steward_messages m ON m.conversation_id=c.id
 WHERE c.user_id=$1::uuid AND c.id=$2::uuid GROUP BY c.id
 RETURNING id::text,conversation_id::text,sequence,role,content::text,input_tokens,output_tokens,created_at`,
		userID, conversationID, uuid.New(), input.Role, input.Content, input.InputTokens, input.OutputTokens))
	if err != nil {
		return Message{}, storeError("追加管家消息", err)
	}
	for position, call := range input.ToolCalls {
		stored, err := insertToolCall(ctx, tx, userID, conversationID, message.ID, position, call)
		if err != nil {
			return Message{}, err
		}
		message.ToolCalls = append(message.ToolCalls, stored)
	}
	if err := touchConversation(ctx, tx, userID, conversationID); err != nil {
		return Message{}, storeError("更新管家消息所属会话", err)
	}
	return message, nil
}

func insertToolCall(ctx context.Context, tx *sql.Tx, userID, conversationID, messageID string, position int, input ToolCallInput) (ToolCall, error) {
	call, err := scanToolCall(tx.QueryRowContext(ctx, `INSERT INTO steward_tool_calls
 (id,message_id,tool_name,input,untrusted,status,position)
 SELECT $4::uuid,m.id,$5,$6::json,$7,'running',$8
 FROM steward_messages m JOIN steward_conversations c ON c.id=m.conversation_id
 WHERE c.user_id=$1::uuid AND c.id=$2::uuid AND m.id=$3::uuid
 RETURNING id::text,message_id::text,tool_name,input::text,output::text,truncated,untrusted,status,error_code,duration_ms,position`,
		userID, conversationID, messageID, uuid.New(), input.Name, input.Input, input.Untrusted, position))
	return call, storeError("追加管家工具调用", err)
}

// FinishToolCall 保存当前用户会话中工具调用的结束结果，并更新会话时间。
func (s *Store) FinishToolCall(ctx context.Context, userID, conversationID, callID string, result ToolCallResult) error {
	if result.Status != "succeeded" && result.Status != "failed" {
		return ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return storeError("开始更新管家工具调用", err)
	}
	defer tx.Rollback()
	if _, err := lockConversation(ctx, tx, userID, conversationID); err != nil {
		return err
	}
	if err := finishToolCall(ctx, tx, userID, conversationID, callID, result); err != nil {
		return err
	}
	if err := touchConversation(ctx, tx, userID, conversationID); err != nil {
		return storeError("更新管家工具调用所属会话", err)
	}
	return storeError("提交管家工具调用结果", tx.Commit())
}

func finishToolCall(ctx context.Context, tx *sql.Tx, userID, conversationID, callID string, result ToolCallResult) error {
	err := requireAffected(tx.ExecContext(ctx, `UPDATE steward_tool_calls t
 SET output=$4::json,truncated=$5,status=$6,error_code=$7,duration_ms=$8
 FROM steward_messages m JOIN steward_conversations c ON c.id=m.conversation_id
 WHERE t.message_id=m.id AND c.user_id=$1::uuid AND c.id=$2::uuid AND t.id=$3::uuid`,
		userID, conversationID, callID, result.Output, result.Truncated, result.Status, result.ErrorCode, result.DurationMS))
	if err != nil {
		return storeError("保存管家工具调用结果", err)
	}
	return nil
}

func readMessages(ctx context.Context, tx *sql.Tx, userID, conversationID string) ([]Message, error) {
	rows, err := tx.QueryContext(ctx, `SELECT m.id::text,m.conversation_id::text,m.sequence,m.role,m.content::text,
 m.input_tokens,m.output_tokens,m.created_at FROM steward_messages m
 JOIN steward_conversations c ON c.id=m.conversation_id
 WHERE c.user_id=$1::uuid AND c.id=$2::uuid ORDER BY m.sequence`, userID, conversationID)
	if err != nil {
		return nil, storeError("读取管家消息", err)
	}
	defer rows.Close()
	messages := []Message{}
	for rows.Next() {
		message, err := scanMessage(rows)
		if err != nil {
			return nil, storeError("读取管家消息内容", err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, storeError("读取管家消息", err)
	}
	return messages, storeError("关闭管家消息读取", rows.Close())
}

func readToolCalls(ctx context.Context, tx *sql.Tx, userID, conversationID string, messages []Message) error {
	rows, err := tx.QueryContext(ctx, `SELECT t.id::text,t.message_id::text,t.tool_name,t.input::text,t.output::text,
 t.truncated,t.untrusted,t.status,t.error_code,t.duration_ms,t.position FROM steward_tool_calls t
 JOIN steward_messages m ON m.id=t.message_id JOIN steward_conversations c ON c.id=m.conversation_id
 WHERE c.user_id=$1::uuid AND c.id=$2::uuid ORDER BY m.sequence,t.position`, userID, conversationID)
	if err != nil {
		return storeError("读取管家工具调用", err)
	}
	defer rows.Close()
	indexes := make(map[string]int, len(messages))
	for i := range messages {
		indexes[messages[i].ID] = i
	}
	for rows.Next() {
		call, err := scanToolCall(rows)
		if err != nil {
			return storeError("读取管家工具调用内容", err)
		}
		index := indexes[call.MessageID]
		messages[index].ToolCalls = append(messages[index].ToolCalls, call)
	}
	if err := rows.Err(); err != nil {
		return storeError("读取管家工具调用", err)
	}
	return storeError("关闭管家工具调用读取", rows.Close())
}

func scanMessage(row scanner) (Message, error) {
	message := Message{ToolCalls: []ToolCall{}}
	err := row.Scan(&message.ID, &message.ConversationID, &message.Sequence, &message.Role,
		(*[]byte)(&message.Content), &message.InputTokens, &message.OutputTokens, &message.CreatedAt)
	message.CreatedAt = message.CreatedAt.UTC()
	return message, err
}

func scanToolCall(row scanner) (ToolCall, error) {
	var call ToolCall
	err := row.Scan(&call.ID, &call.MessageID, &call.Name, (*[]byte)(&call.Input), (*[]byte)(&call.Output),
		&call.Truncated, &call.Untrusted, &call.Status, &call.ErrorCode, &call.DurationMS, &call.Position)
	return call, err
}
