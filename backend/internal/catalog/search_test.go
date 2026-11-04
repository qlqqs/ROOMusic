package catalog

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

func TestSearchBoundedTotal(t *testing.T) {
	for _, tc := range []struct {
		count, total int64
		relation     string
	}{{0, 0, "eq"}, {999, 999, "eq"}, {1000, 1000, "eq"}, {1001, 1000, "gte"}} {
		total, relation := boundedSearchTotal(tc.count)
		if total != tc.total || relation != tc.relation {
			t.Fatalf("count %d: %d %s", tc.count, total, relation)
		}
	}
}

func TestSearchDTORequiredNullsAndTrackSummary(t *testing.T) {
	data, err := json.Marshal(SearchTrackItem{})
	if err != nil {
		t.Fatal(err)
	}
	var item map[string]json.RawMessage
	if err := json.Unmarshal(data, &item); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"track_id", "title", "artist", "present", "duration_seconds", "group_id", "release", "source", "match"} {
		if _, ok := item[name]; !ok {
			t.Errorf("missing %s: %s", name, data)
		}
	}
	var match map[string]json.RawMessage
	if err := json.Unmarshal(item["match"], &match); err != nil {
		t.Fatal(err)
	}
	if len(match) != 8 {
		t.Fatalf("match keys: %s", item["match"])
	}
	for _, name := range []string{"role", "artist_id", "alias", "release_id", "track_id"} {
		if string(match[name]) != "null" {
			t.Errorf("missing nullable %s: %s", name, item["match"])
		}
	}
	for _, name := range []string{"relative_path", "source_root_id", "content_fingerprint", "source_kind"} {
		if _, ok := item[name]; ok {
			t.Errorf("unexpected top-level %s", name)
		}
	}
}

func TestSearchPlanBoundedCountAndLiteralPatterns(t *testing.T) {
	if err := textnorm.LoadEmbedded(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"albums", "tracks", "releases"} {
		for _, mode := range []string{"legacy", "normalized"} {
			plan := newSearchPlan("Ａ%_~", kind, mode, 3)
			count := plan.countSQL(true, false)
			if !strings.Contains(count, "LIMIT 1001") || strings.Contains(count, "COUNT(*) OVER") || strings.Contains(count, "SELECT DISTINCT") {
				t.Fatalf("unbounded entity count: %s", count)
			}
			if kind == "albums" || kind == "releases" {
				if !strings.Contains(count, "matching_releases AS NOT MATERIALIZED") || !strings.Contains(count, "LIMIT 1001") {
					t.Fatalf("group/release count must probe unique base entities from a narrow matched-release set: %s", count)
				}
				if kind == "albums" && !strings.Contains(count, "FROM release_groups g WHERE") {
					t.Fatalf("album count must deduplicate on release-group identity: %s", count)
				}
				if kind == "releases" && mode == "legacy" && !strings.Contains(count, "SELECT releases.id FROM release_effective releases") {
					t.Fatalf("release count must deduplicate on release identity: %s", count)
				}
			}
			if strings.Contains(count, "matching_releases AS MATERIALIZED") || strings.Contains(count, "GROUP BY") || strings.Contains(count, "\n\t\t UNION\n") {
				t.Fatalf("count must not materialize/deduplicate the complete entity set: %s", count)
			}
			if strings.Contains(count, "Ａ%_~") {
				t.Fatal("query was interpolated")
			}
			if mode == "normalized" {
				if plan.args[0] != "a%_~" || plan.args[1] != "a~%~_~~" || plan.args[2] != "a%" {
					t.Fatalf("pattern arguments: %#v", plan.args)
				}
				if !strings.Contains(plan.pageSQL(false), `COLLATE "C"`) {
					t.Fatal("missing deterministic normalized order")
				}
			}
		}
	}
	groupPlan := newSearchPlan("hot", "albums", "normalized", 1)
	groupPage := groupPlan.pageSQL(false)
	if !strings.Contains(groupPage, "representatives AS MATERIALIZED (") ||
		!strings.Contains(groupPage, "SELECT DISTINCT ON (releases.group_id)") ||
		!strings.Contains(groupPage, "releases.group_id IN (SELECT entity_id FROM tier0_entities)") ||
		!strings.Contains(groupPage, "FROM tier0_representatives b") {
		t.Fatal("album page must select one visible representative per matched group in a single pass")
	}
	for _, kind := range []string{"albums", "tracks", "releases"} {
		page := newSearchPlan("hot", kind, "normalized", 1).pageSQL(false)
		if !strings.Contains(page, "title_values AS MATERIALIZED") || !strings.Contains(page, "page AS MATERIALIZED") ||
			strings.Index(page, "LIMIT $8 OFFSET $9") > strings.Index(page, "page_sources AS MATERIALIZED") ||
			!strings.Contains(page, "tier2_page AS MATERIALIZED") {
			t.Fatalf("ranking must paginate complete entity ranks before retrieving descriptions: %s", page)
		}
	}

	plan := newSearchPlan("-", "tracks", "normalized", 1)
	if plan.empty || plan.args[3] != "" || !strings.Contains(plan.hits, "AND $4<>''") {
		t.Fatal("empty catalog-number key must not match every number")
	}
}

// 密集路径必须先读取当前来源字段，不能把全局热门GIN检索放进逐实体循环。
func TestSearchDenseSubjectPredicateBoundary(t *testing.T) {
	for _, kind := range []string{"albums", "tracks", "releases"} {
		plan := searchPlan{kind: kind}
		query := plan.normalizedEntityMatchSQL("entity.id", "placement.release_id", "v.normalized_value LIKE $1")
		for _, relation := range []string{"track_id=", "release_id="} {
			if !strings.Contains(query, "SELECT field,normalized_value,projection_generation,short_grams FROM catalog_search_values WHERE "+relation) {
				t.Fatalf("%s缺少来源访问边界：%s", kind, query)
			}
		}
		if strings.Count(query, " OFFSET 0) v\n WHERE v.normalized_value LIKE $1") != 2 {
			t.Fatalf("%s搜索残差必须位于来源访问之外：%s", kind, query)
		}
	}
}
