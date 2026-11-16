package stewardtools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/platform/postgres/postgrestest"
)

func TestPostgreSQLStewardCatalogC7ReviewEvidenceSafeAndReadOnly(t *testing.T) {
	db, group, release, _, _ := catalogToolFixture(t)
	ctx := context.Background()
	before, err := postgrestest.Snapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		input map[string]string
		want  int
	}{
		{"release", map[string]string{"release_id": release}, 1},
		{"group", map[string]string{"group_id": group}, 1},
		{"both", map[string]string{"release_id": release, "group_id": group}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input, _ := json.Marshal(tc.input)
			targets, err := ReadReviewTargets(ctx, catalog.NewStore(db), governance.NewStore(db), input)
			if err != nil || len(targets) != tc.want {
				t.Fatalf("targets=%+v err=%v", targets, err)
			}
			for _, target := range targets {
				if target.Source != "catalog_read_summary" || target.ID == "" || target.Revision < 1 {
					t.Errorf("摘要缺少来源与版本：%+v", target)
				}
			}
			raw, err := json.Marshal(targets)
			if err != nil {
				t.Fatal(err)
			}
			assertSafeCatalogOutput(t, raw)
			for _, private := range []string{"relative_path", "relative_directory", "/synthetic-only/", "album/01.flac", "synthetic-private-lyrics", "lyrics_source", "candidates", "reviewer_id"} {
				if strings.Contains(string(raw), private) {
					t.Errorf("安全摘要泄露 %s: %s", private, raw)
				}
			}
			if tc.name != "group" {
				for _, field := range []string{`"catalog_effective_untrusted"`, `"fields"`, `"open_issues"`, `"truncated"`} {
					if !strings.Contains(string(raw), field) {
						t.Errorf("缺少证据字段 %s", field)
					}
				}
			}
		})
	}
	after, err := postgrestest.Snapshot(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if diff := postgrestest.Diff(before, after); len(diff) != 0 {
		t.Fatal("审查证据读取修改数据库", diff)
	}
}
