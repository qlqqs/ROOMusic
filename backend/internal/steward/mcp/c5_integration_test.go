package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
)

func TestPostgreSQLMCPC5PersonalTokenScopes(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			f := newMCPFixture(t)
			for _, scope := range []token.Scope{token.ScopeRead, token.ScopeReadWrite} {
				t.Run(string(scope), func(t *testing.T) {
					_, plain, err := f.store.Issue(context.Background(), f.users["user"].ID, token.IssueInput{Name: "C5 合成令牌", Scope: scope})
					if err != nil {
						t.Fatal(err)
					}
					f.plaintext["user"] = plain
					ctx, session := connectMCP(t, f, "user", version)
					list, err := session.ListTools(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, d := range list.Tools {
						if d.Name == "synthetic_personal" {
							found = true
							if d.Annotations != nil && d.Annotations.ReadOnlyHint {
								t.Error("个人写工具标为只读")
							}
						}
					}
					if found != (scope == token.ScopeReadWrite) {
						t.Fatalf("scope=%s personal visible=%v", scope, found)
					}
					result := callMCP(t, ctx, session, "synthetic_personal")
					if scope == token.ScopeRead {
						requireToolError(t, result, "permission_denied")
					} else if result.IsError || !strings.Contains(resultText(t, result), f.users["user"].ID) {
						t.Fatalf("个人写调用失败：%+v", result)
					}
				})
			}
		})
	}
}

func TestPostgreSQLMCPC5InvalidInputKeepsHandlerMessage(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		t.Run(version, func(t *testing.T) {
			f := newMCPFixture(t)
			const message = "track_ids 与 release_id 必须二选一"
			if err := f.registry.Register(tool.Definition{Name: "synthetic_invalid_personal", Scope: tool.ScopePersonal, MinRole: tool.RoleMember,
				Input: json.RawMessage(`{"type":"object","additionalProperties":false}`), Output: json.RawMessage(`{"type":"object"}`),
				Handler: func(context.Context, tool.Caller, json.RawMessage) (json.RawMessage, error) {
					return nil, &tool.Error{Code: tool.CodeInvalidInput, Message: message}
				}}); err != nil {
				t.Fatal(err)
			}
			_, plain, err := f.store.Issue(context.Background(), f.users["user"].ID, token.IssueInput{Name: "合成校验令牌", Scope: token.ScopeReadWrite})
			if err != nil {
				t.Fatal(err)
			}
			f.plaintext["user"] = plain
			ctx, session := connectMCP(t, f, "user", version)
			result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "synthetic_invalid_personal", Arguments: map[string]any{}})
			if err != nil {
				t.Fatal(err)
			}
			requireToolError(t, result, string(tool.CodeInvalidInput))
			var got struct{ Message string }
			if err := json.Unmarshal([]byte(resultText(t, result)), &got); err != nil || got.Message != message {
				t.Fatalf("MCP 丢失处理函数说明：%+v %v", result, err)
			}
			if strings.Contains(f.logs.String(), message) {
				t.Fatal("处理函数说明进入日志")
			}
		})
	}
}
