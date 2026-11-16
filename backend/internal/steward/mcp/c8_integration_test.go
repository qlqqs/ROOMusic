package mcp

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/steward/secret"
	"github.com/qlqq/roomusic/backend/internal/steward/web"
)

func TestPostgreSQLC8MCPAvailableSearch(t *testing.T) {
	f := newMCPFixture(t)
	service := web.New(f.settings, secret.New(t.TempDir()+"/key"), nil, nil)
	for _, d := range service.StewardTools() {
		if err := f.registry.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	for _, source := range []string{"none", "searxng", "tavily", "native"} {
		t.Run(source, func(t *testing.T) {
			if _, err := f.db.Exec(`INSERT INTO steward_user_settings(user_id,search_source) VALUES($1,$2) ON CONFLICT(user_id) DO UPDATE SET search_source=$2`, f.users["admin"].ID, source); err != nil {
				t.Fatal(err)
			}
			ctx, session := connectMCP(t, f, "admin", "2025-11-25")
			list, err := session.ListTools(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, d := range list.Tools {
				if d.Name == "web_search" {
					found = true
				}
			}
			if found != (source == "searxng" || source == "tavily") {
				t.Fatalf("来源=%s 可用=%v", source, found)
			}
		})
	}
}
