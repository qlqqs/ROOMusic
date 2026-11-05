package app

import (
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// 只布置原始目录事实；署名与身份投影由真实维护入口建立，默认版本由治理 HTTP 设置。
func prepareAdjudicationAlbums(f *artistSamples) {
	t := f.t
	// 未参与的版本加入同一组，后面设为默认，防止参与过滤偷换代表版本。
	mustExec(t, f.app, `UPDATE releases SET group_id=$2::uuid WHERE id=$1::uuid`, f.releases[3].releaseID, f.releases[0].groupID)
	f.releases[3].groupID = f.releases[0].groupID
	for _, role := range []string{"composer", "performer"} {
		mustExec(t, f.app, `INSERT INTO track_credits(track_id,role,name,position) VALUES($1::uuid,$2,'邓丽君',1)`, f.releases[1].trackIDs[0], role)
	}
	// 三种艺人严格分离，不能用本来同时带两个发行级角色的艺人假充筛选证据。
	var rootID string
	if err := f.app.database.QueryRow(`SELECT source_root_id::text FROM releases WHERE id=$1::uuid`, f.releases[0].releaseID).Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	r := insertSearchRelease(t, f.app, rootID, groupedRelease{title: "独立发行署名", artist: "Alpha Singer", tracks: []string{"present"}})
	mustExec(t, f.app, `UPDATE releases SET album_artist='Beta Singer',created_at='2026-02-01T00:00:00Z' WHERE id=$1::uuid`, r.releaseID)
	mustExec(t, f.app, `UPDATE tracks SET artist='Gamma Singer' WHERE id=$1::uuid`, r.trackIDs[0])
}

func recordAdjudicationAlbums(f *artistSamples) {
	t := f.t
	base := "/api/v1/artists/" + f.primary + "/albums"
	adjudicationSuccess(f, "POST", defaultPath(f.releases[0].groupID), defaultBody(1, f.releases[3].releaseID))
	baseline := adjudicationGet(f, "albums-default", base)
	original := artistSampleItems(t, baseline, 1)[0].(map[string]any)
	assertAdjudicationAlbum(t, original, f.releases[0].groupID, f.releases[0].releaseID, f.releases[3].releaseID)
	if _, exists := original["roles"]; exists {
		t.Fatal("默认专辑列表不得新增 roles 字段")
	}
	all := adjudicationGet(f, "albums-any", base+"?participation=any")
	items := artistSampleItems(t, all, 2)
	seen := map[string]bool{}
	for _, raw := range items {
		item := raw.(map[string]any)
		id := item["group"].(map[string]any)["id"].(string)
		if seen[id] {
			t.Fatal("同组多角色重复返回专辑")
		}
		seen[id] = true
		switch id {
		case f.releases[0].groupID:
			assertAdjudicationAlbum(t, item, id, f.releases[0].releaseID, f.releases[3].releaseID)
			assertAdjudicationRoles(t, item["roles"], "release_artist", "album_artist", "track_artist")
		case f.releases[1].groupID:
			assertAdjudicationAlbum(t, item, id, f.releases[1].releaseID, f.releases[1].releaseID)
			assertAdjudicationRoles(t, item["roles"], "composer", "performer")
		default:
			t.Fatalf("返回未参与专辑：%v", item)
		}
	}
	query := "q=" + url.QueryEscape("英文现场")
	filtered := adjudicationGet(f, "albums-any-query", base+"?participation=any&"+query)
	item := artistSampleItems(t, filtered, 1)[0].(map[string]any)
	assertAdjudicationAlbum(t, item, f.releases[1].groupID, f.releases[1].releaseID, f.releases[1].releaseID)
	artistSampleItems(t, adjudicationGet(f, "albums-default-query", base+"?"+query), 0)
	page := adjudicationGet(f, "albums-any-page", base+"?participation=any&page_size=1&page=2")
	paged := artistSampleItems(t, page, 1)
	if !reflect.DeepEqual(paged[0], items[1]) || page["pagination"].(map[string]any)["total"] != float64(2) {
		t.Fatalf("参与列表分页或排序漂移：%v", page)
	}
	artistSampleItems(t, adjudicationGet(f, "albums-any-empty", base+"?participation=any&q=不存在的专辑"), 0)
	if after := adjudicationGet(f, "albums-default-after-any", base); !reflect.DeepEqual(after, baseline) {
		t.Fatalf("参与或搜索读取改变了默认列表：before=%v after=%v", baseline, after)
	}
	for _, tc := range []struct{ name, query, code string }{
		{"invalid-participation", "participation=all", "invalid_artist_participation"},
		{"empty-participation", "participation=", "invalid_artist_participation"},
		{"repeated-participation", "participation=any&participation=any", "invalid_artist_participation"},
		{"repeated-query", "q=a&q=b", "invalid_query"},
		{"long-query", "q=" + strings.Repeat("a", 201), "invalid_query"},
		{"invalid-pagination", "participation=any&page_size=101", "invalid_pagination"},
	} {
		adjudicationError(f, "albums-"+tc.name, f.call("GET", base+"?"+tc.query, nil, &f.member), 400, tc.code)
	}
}

func assertAdjudicationAlbum(t *testing.T, item map[string]any, groupID, creditedID, representativeID string) {
	t.Helper()
	group := item["group"].(map[string]any)
	if group["id"] != groupID || item["credited_release_id"] != creditedID || group["representative"].(map[string]any)["id"] != representativeID {
		t.Fatalf("参与版本或默认代表版本不符：%v", item)
	}
}

func assertAdjudicationRoles(t *testing.T, value any, expected ...string) {
	t.Helper()
	roles, ok := value.([]any)
	if !ok || len(roles) != len(expected) {
		t.Fatalf("角色词表不符：%v，期望 %v", value, expected)
	}
	seen := map[string]bool{}
	for _, role := range roles {
		text, ok := role.(string)
		if !ok || seen[text] {
			t.Fatalf("角色重复或无效：%v", roles)
		}
		seen[text] = true
	}
	for _, role := range expected {
		if !seen[role] {
			t.Fatalf("角色缺少 %s：%v", role, roles)
		}
	}
}

func recordAdjudicationRoles(f *artistSamples) {
	for _, tc := range []struct {
		name, role string
		expected   map[string]string
	}{
		{"role-album-artist", "album_artist", map[string]string{f.id(9): "release_artist", f.id(10): "album_artist"}},
		{"role-release-artist", "release_artist", map[string]string{f.id(9): "release_artist"}},
	} {
		body := adjudicationGet(f, tc.name, "/api/v1/artists?q=Singer&role="+tc.role)
		items := artistSampleItems(f.t, body, len(tc.expected))
		seen := map[string]bool{}
		for _, raw := range items {
			item := raw.(map[string]any)
			id := item["id"].(string)
			role, ok := tc.expected[id]
			if !ok || seen[id] {
				f.t.Fatalf("发行级角色筛选混入其他艺人或重复：%v", body)
			}
			seen[id] = true
			assertAdjudicationRoles(f.t, item["roles"], role)
		}
	}
}
