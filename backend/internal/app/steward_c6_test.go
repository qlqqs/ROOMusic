package app

import (
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	governancetools "github.com/qlqq/roomusic/backend/internal/catalog/governance/stewardtools"
	catalogtools "github.com/qlqq/roomusic/backend/internal/catalog/stewardtools"
	librarytools "github.com/qlqq/roomusic/backend/internal/library/stewardtools"
	listeningtools "github.com/qlqq/roomusic/backend/internal/listening/stewardtools"
	operationtools "github.com/qlqq/roomusic/backend/internal/operations/stewardtools"
	personaltools "github.com/qlqq/roomusic/backend/internal/personal/stewardtools"
	"github.com/qlqq/roomusic/backend/internal/steward/tool"
	zonestools "github.com/qlqq/roomusic/backend/internal/zones/stewardtools"
)

func TestC6RealRegistryToolCounts(t *testing.T) {
	defs := catalogtools.StewardTools(catalog.NewStore(nil), governance.NewStore(nil))
	defs = append(defs, governancetools.StewardTools(nil, nil)...)
	defs = append(defs, operationtools.RevertTools(nil)...)
	defs = append(defs, operationtools.StewardTools(nil)...)
	defs = append(defs, librarytools.StewardTools(nil)...)
	defs = append(defs, zonestools.StewardTools(nil, catalog.NewStore(nil))...)
	defs = append(defs, listeningtools.StewardTools(nil)...)
	defs = append(defs, personaltools.StewardTools(nil)...)
	seen := map[string]bool{}
	shared := 0
	for _, d := range defs {
		if seen[d.Name] {
			t.Fatalf("重复工具：%s", d.Name)
		}
		seen[d.Name] = true
		if d.Scope == tool.ScopeShared {
			shared++
		}
	}
	if len(defs) != 52 || shared != 12 {
		t.Fatalf("工具总数=%d shared=%d，期望 52 / 12", len(defs), shared)
	}
}
