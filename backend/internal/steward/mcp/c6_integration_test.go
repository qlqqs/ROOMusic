package mcp

import (
	"context"
	"encoding/json"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	governancetools "github.com/qlqq/roomusic/backend/internal/catalog/governance/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/steward/token"
)

func TestPostgreSQLMCPC6SharedScopesAndOrigin(t *testing.T) {
	for _, version := range []string{"2026-07-28", "2025-11-25"} {
		for _, tc := range []struct {
			role  string
			scope token.Scope
		}{{"admin", token.ScopeRead}, {"admin", token.ScopeReadWrite}, {"user", token.ScopeReadWrite}} {
			t.Run(version+"/"+tc.role+"/"+string(tc.scope), func(t *testing.T) {
				f := newMCPFixture(t)
				if err := maintenance.Initialize(context.Background(), f.db); err != nil {
					t.Fatal(err)
				}
				for _, d := range governancetools.StewardTools(f.db, nil) {
					if err := f.registry.Register(d); err != nil {
						t.Fatal(err)
					}
				}
				exec := func(q string, args ...any) {
					t.Helper()
					if _, err := f.db.Exec(q, args...); err != nil {
						t.Fatal(err)
					}
				}
				root, g, r, m := uuid.New(), uuid.New(), uuid.New(), uuid.New()
				exec(`INSERT INTO library_roots(id,path) VALUES($1,'/synthetic-c6')`, root)
				exec(`INSERT INTO release_groups(id) VALUES($1)`, g)
				exec(`INSERT INTO releases(id,group_id,title,artist) VALUES($1,$2,'原名','合成')`, r, g)
				exec(`INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, m, r)
				exec(`INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,source_status,observed_at) VALUES($1,$2,1,'合成','合成',$3,'c6.wav','present',now())`, uuid.New(), m, root)
				entry, plain, err := f.store.Issue(context.Background(), f.users[tc.role].ID, token.IssueInput{Name: "C6 合成", Scope: tc.scope})
				if err != nil {
					t.Fatal(err)
				}
				f.plaintext[tc.role] = plain
				ctx, session := connectMCP(t, f, tc.role, version)
				listed, err := session.ListTools(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				visible := false
				for _, d := range listed.Tools {
					if d.Name == "correct_release" {
						visible = true
						if d.Annotations != nil && d.Annotations.ReadOnlyHint {
							t.Error("写工具标成只读")
						}
					}
				}
				allowed := tc.role == "admin" && tc.scope == token.ScopeReadWrite
				if visible != allowed {
					t.Fatalf("shared visible=%v want=%v", visible, allowed)
				}
				result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: "correct_release", Arguments: map[string]any{"release_id": r, "expected_revision": 1, "changes": map[string]any{"title": map[string]any{"mode": "set", "value": "MCP 新名"}}}})
				if err != nil {
					t.Fatal(err)
				}
				var n int
				if err := f.db.QueryRow(`SELECT count(*) FROM operations`).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if !allowed {
					requireToolError(t, result, "permission_denied")
					if n != 0 {
						t.Fatal("拒绝后产生操作")
					}
					var title string
					f.db.QueryRow(`SELECT title FROM release_effective WHERE id=$1`, r).Scan(&title)
					if title != "原名" {
						t.Fatal(title)
					}
					return
				}
				if result.IsError || n != 1 {
					t.Fatal(resultText(t, result), n)
				}
				var origin struct {
					Source  string `json:"source"`
					TokenID string `json:"token_id"`
				}
				var raw []byte
				if err := f.db.QueryRow(`SELECT origin FROM operations`).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &origin); err != nil || origin.Source != "mcp" || origin.TokenID != entry.ID {
					t.Fatalf("origin=%s err=%v", raw, err)
				}
			})
		}
	}
}
