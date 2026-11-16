// Package stewardtools 将成员管理服务适配为管理员工具。
package stewardtools

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

type input struct {
	MemberID string `json:"member_id"`
	Disabled bool   `json:"disabled"`
}

func decode(raw json.RawMessage) (input, error) {
	var p input
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	if !uuid.Valid(p.MemberID) {
		return p, &tool.Error{Code: tool.CodeInvalidInput, Message: "成员标识必须是 UUID"}
	}
	p.MemberID = strings.ToLower(p.MemberID)
	return p, nil
}
func failure(err error) error {
	var typed *identity.UserManagementError
	if errors.As(err, &typed) {
		return &tool.Error{Code: tool.ErrorCode(typed.Code), Message: typed.Message}
	}
	return err
}
func StewardTools(db *sql.DB, logger *slog.Logger) []tool.Definition {
	definitions := []tool.Definition{{Name: "list_members", Description: "列出成员的 ID、用户名、角色、停用状态和创建时间，不含凭据与会话。", MinRole: tool.RoleAdmin, Scope: tool.ScopeRead, Untrusted: true,
		Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`), Output: json.RawMessage(`{"type":"object"}`), Handler: func(ctx context.Context, _ tool.Caller, _ json.RawMessage) (json.RawMessage, error) {
			items, err := identity.ListUsers(ctx, db)
			if err != nil {
				return nil, failure(err)
			}
			return json.Marshal(map[string]any{"items": items})
		}}}
	for _, name := range []string{"set_member_disabled", "revoke_member_sessions"} {
		properties := `"member_id":{"type":"string","minLength":36,"maxLength":36}`
		required := `"member_id"`
		description := "撤销指定成员的全部登录会话，不返回会话标识。不可撤销。"
		if name == "set_member_disabled" {
			properties += `,"disabled":{"type":"boolean"}`
			required += `,"disabled"`
			description = "停用或启用成员，不能停用最后一个管理员。停用会撤销该成员登录会话。不可撤销。"
		}
		definitions = append(definitions, tool.Definition{Name: name, Description: description, MinRole: tool.RoleAdmin, Scope: tool.ScopeShared, Revertible: false, Untrusted: true,
			Input: json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{` + properties + `},"required":[` + required + `]}`), Output: json.RawMessage(`{"type":"object"}`),
			Preview: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (tool.Preview, error) {
				p, err := decode(raw)
				if err != nil {
					return tool.Preview{}, err
				}
				if strings.EqualFold(p.MemberID, caller.User.ID) {
					return tool.Preview{}, selfTargetError()
				}
				preview := tool.Preview{Target: p.MemberID, Changes: []tool.Change{}, Summary: description}
				members, err := identity.ListUsers(ctx, db)
				if err != nil {
					return preview, failure(err)
				}
				for _, member := range members {
					if member.ID == p.MemberID {
						preview.Target = member.Username + "（" + member.Role + "）"
						if name == "set_member_disabled" {
							preview.Changes = []tool.Change{{Field: "disabled", Before: member.Disabled, After: p.Disabled}}
						} else {
							preview.Changes = []tool.Change{{Field: "action", Before: nil, After: description}}
						}
						return preview, nil
					}
				}
				return preview, &tool.Error{Code: tool.CodeTargetNotFound, Message: "成员不存在"}
			},
			Handler: func(ctx context.Context, caller tool.Caller, raw json.RawMessage) (json.RawMessage, error) {
				p, err := decode(raw)
				if err != nil {
					return nil, err
				}
				if strings.EqualFold(p.MemberID, caller.User.ID) {
					return nil, selfTargetError()
				}
				out := map[string]any{"member_id": p.MemberID}
				if name == "set_member_disabled" {
					err = identity.SetUserDisabled(ctx, db, p.MemberID, p.Disabled)
					out["disabled"] = p.Disabled
				} else {
					err = identity.RevokeUserSessions(ctx, db, p.MemberID)
					out["revoked"] = true
				}
				if err != nil {
					return nil, failure(err)
				}
				tool.LogConfigChanged(ctx, logger, caller, name, p.MemberID)
				return json.Marshal(out)
			},
		})
	}
	return definitions
}

func selfTargetError() error {
	return &tool.Error{Code: "self_target_not_allowed", Message: "不能通过管家对自己执行此成员操作，请到设置页操作"}
}
