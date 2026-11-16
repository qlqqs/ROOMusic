package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/qlqq/roomusic/backend/internal/steward/memory"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
)

func TestPostgreSQLStewardC10MCPMemoryReadOnlyAndIsolation(t *testing.T) {
	f := newMCPFixture(t)
	memories := memory.NewStore(f.db)
	for _, d := range memories.StewardTools() {
		if err := f.registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	item, err := memories.Remember(context.Background(), f.users["admin"].ID, nil, "甲的私有记忆")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []token.Scope{token.ScopeRead, token.ScopeReadWrite} {
		t.Run(string(scope), func(t *testing.T) {
			_, plain, err := f.store.Issue(context.Background(), f.users["user"].ID, token.IssueInput{Name: "C10 合成", Scope: scope})
			if err != nil {
				t.Fatal(err)
			}
			f.plaintext["user"] = plain
			ctx, session := connectMCP(t, f, "user", "2025-11-25")
			listed, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			visible := map[string]bool{}
			for _, d := range listed.Tools {
				visible[d.Name] = true
				if d.Name == "list_memories" && (d.Annotations == nil || !d.Annotations.ReadOnlyHint) {
					t.Error("记忆列表未标只读")
				}
			}
			if !visible["list_memories"] || visible["remember"] != (scope == token.ScopeReadWrite) || visible["forget"] != (scope == token.ScopeReadWrite) {
				t.Fatal(visible)
			}
			result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "list_memories", Arguments: map[string]any{}})
			if err != nil || result.IsError || strings.Contains(resultText(t, result), "甲的私有记忆") {
				t.Fatal(result, err)
			}
			for _, name := range []string{"remember", "forget"} {
				args := map[string]any{"id": item.ID}
				if name == "remember" {
					args["content"] = "越权更新"
				}
				result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
				if err != nil {
					t.Fatal(err)
				}
				code := "permission_denied"
				if scope == token.ScopeReadWrite {
					code = "not_found"
				}
				requireToolError(t, result, code)
			}
			if scope == token.ScopeReadWrite {
				result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "remember", Arguments: map[string]any{"content": ""}})
				if err != nil {
					t.Fatal(err)
				}
				requireToolError(t, result, "invalid_input")
				result, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "remember", Arguments: map[string]any{"content": "乙的合成偏好"}})
				if err != nil || result.IsError {
					t.Fatal(result, err)
				}
				var own memory.Item
				if err := json.Unmarshal([]byte(resultText(t, result)), &own); err != nil || own.ID == "" {
					t.Fatal(result, err)
				}
				result, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "remember", Arguments: map[string]any{"id": own.ID, "content": "乙的更新偏好"}})
				if err != nil || result.IsError || !strings.Contains(resultText(t, result), "乙的更新偏好") {
					t.Fatal(result, err)
				}
				result, err = session.CallTool(ctx, &sdk.CallToolParams{Name: "forget", Arguments: map[string]any{"id": own.ID}})
				if err != nil || result.IsError {
					t.Fatal(result, err)
				}
				ownList, err := memories.List(ctx, f.users["user"].ID)
				if err != nil || len(ownList.Items) != 0 {
					t.Fatal(ownList, err)
				}
				adminList, err := memories.List(ctx, f.users["admin"].ID)
				if err != nil || len(adminList.Items) != 1 || adminList.Items[0].ID != item.ID {
					t.Fatal("其他用户记忆被改变", adminList, err)
				}
			}
		})
	}
}
