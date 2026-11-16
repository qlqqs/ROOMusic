package memory

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func (s *Store) StewardTools() []tool.Definition {
	definition := func(name, description, properties, required string, run func(context.Context, string, input) (any, error)) tool.Definition {
		return tool.Definition{Name: name, Description: description, MinRole: tool.RoleMember, Scope: tool.ScopePersonal, ReadOnly: name == "list_memories", InvalidInputCode: "invalid_input", Untrusted: true,
			Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + properties + `},"required":[` + required + `]}`), Output: json.RawMessage(`{"type":"object"}`),
			Handler: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
				var p input
				if err := json.Unmarshal(raw, &p); err != nil {
					return nil, memoryToolError(ErrInvalidInput)
				}
				value, err := run(ctx, caller.User.ID, p)
				if err != nil {
					return nil, memoryToolError(err)
				}
				return json.Marshal(value)
			}}
	}
	return []tool.Definition{
		definition("remember", "记住用户明确要求保存的偏好。content 为 1 至 500 字，每人最多 50 条；提供 id 时更新本人已有记忆，省略时新增。返回 id、content、created_at、updated_at。", `"content":{"type":"string","minLength":1,"maxLength":500},"id":{"type":"string","minLength":36,"maxLength":36}`, `"content"`, func(ctx context.Context, user string, p input) (any, error) {
			return s.Remember(ctx, user, p.ID, p.Content)
		}),
		definition("forget", "删除本人一条记忆，id 来自 list_memories。返回 deleted。", `"id":{"type":"string","minLength":36,"maxLength":36}`, `"id"`, func(ctx context.Context, user string, p input) (any, error) {
			return map[string]bool{"deleted": true}, s.Forget(ctx, user, p.ID)
		}),
		definition("list_memories", "只读列出本人记忆，按更新时间倒序，返回 items、limit、max_length。", "", "", func(ctx context.Context, user string, _ input) (any, error) { return s.List(ctx, user) }),
	}
}

type input struct {
	ID      *string `json:"id"`
	Content string  `json:"content"`
}

func memoryToolError(err error) error {
	switch {
	case errors.Is(err, ErrInvalidInput):
		return &tool.Error{Code: "invalid_input", Message: "记忆内容须为 1 至 500 字，标识须为 UUID"}
	case errors.Is(err, ErrLimitReached):
		return &tool.Error{Code: "memory_limit_reached", Message: "最多保存 50 条记忆"}
	case errors.Is(err, ErrNotFound):
		return &tool.Error{Code: tool.CodeTargetNotFound, Message: "记忆不存在"}
	default:
		return err
	}
}
