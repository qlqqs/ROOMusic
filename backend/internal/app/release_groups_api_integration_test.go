package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// groupedRelease 描述直接插入的一个 Release：groupID 为空时新建组，year 为 nil 表示没有年份，
// tracks 里每项是一条曲目的状态（present 或 missing），uncertain 是不确定字段决定的条数（各计一次 attention）。
type groupedRelease struct {
	id        string
	groupID   string
	title     string
	artist    string
	year      any
	createdAt string
	tracks    []string
	uncertain int
}

func insertGroupedRelease(t *testing.T, application *integrationTestApplication, rootID string, spec groupedRelease) (releaseID, groupID string) {
	t.Helper()
	releaseID, groupID = spec.id, spec.groupID
	if releaseID == "" {
		releaseID = uuid.New()
	}
	if groupID == "" {
		groupID = uuid.New()
		if _, err := application.database.Exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, groupID); err != nil {
			t.Fatalf("insert release group: %v", err)
		}
	}
	createdAt := spec.createdAt
	if createdAt == "" {
		createdAt = "2026-01-01T00:00:00Z"
	}
	if _, err := application.database.Exec(`INSERT INTO releases(id,group_id,title,artist,year,created_at,source_root_id,relative_directory,candidate_anchor,candidate_kind)
		VALUES($1::uuid,$2::uuid,$3,$4,$5,$6::timestamptz,$7::uuid,$8,$9,'ordinary_directory')`,
		releaseID, groupID, spec.title, spec.artist, spec.year, createdAt, rootID, spec.title, rootID+":v2:"+releaseID); err != nil {
		t.Fatalf("insert release %q: %v", spec.title, err)
	}
	mediumID := uuid.New()
	if _, err := application.database.Exec(`INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,1,'Medium')`, mediumID, releaseID); err != nil {
		t.Fatalf("insert medium: %v", err)
	}
	for index, status := range spec.tracks {
		trackID := uuid.New()
		if _, err := application.database.Exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity)
			VALUES($1::uuid,$2::uuid,$3,$4,$5,1,$6::uuid,$7,$8,NOW(),'physical',$9)`,
			trackID, mediumID, index+1, fmt.Sprintf("%s track %d", spec.title, index+1), spec.artist, rootID, releaseID+"/"+fmt.Sprint(index)+".flac", status, rootID+":physical:v1:"+trackID); err != nil {
			t.Fatalf("insert track: %v", err)
		}
	}
	for index := 0; index < spec.uncertain; index++ {
		if _, err := application.database.Exec(`INSERT INTO release_field_decisions(release_id,field_key,selected_value,selected_source,confidence,action,rule_id,candidates,reason)
			VALUES($1::uuid,$2,to_jsonb('x'::text),'tag','medium','uncertain_apply','majority_v1',to_jsonb(ARRAY['x','y']::text[]),'inconsistent_candidates')`,
			releaseID, fmt.Sprintf("field_%d", index)); err != nil {
			t.Fatalf("insert uncertain decision: %v", err)
		}
	}
	return releaseID, groupID
}

type releaseListResponse struct {
	Items      []catalog.ReleaseSummary `json:"items"`
	Pagination struct {
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
		Total    int64 `json:"total"`
	} `json:"pagination"`
}

type releaseGroupListResponse struct {
	Items      []catalog.ReleaseGroupSummary `json:"items"`
	Pagination struct {
		Page     int   `json:"page"`
		PageSize int   `json:"page_size"`
		Total    int64 `json:"total"`
	} `json:"pagination"`
}

func getJSON(t *testing.T, application *integrationTestApplication, user *integrationTestUser, path string, target any) {
	t.Helper()
	response := application.request(t, http.MethodGet, path, nil, user, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d: %s", path, response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), target); err != nil {
		t.Fatalf("decode GET %s: %v", path, err)
	}
}

func TestPostgreSQLReleaseGroupListMirrorsReleaseList(t *testing.T) {
	application := newIntegrationTestApplication(t)
	user := application.createUser(t, "user")
	rootID := uuid.New()
	if _, err := application.database.Exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, application.allowedRoot); err != nil {
		t.Fatalf("insert root fixture: %v", err)
	}

	// 每个 Release 一个组（M3 人工归组之前的现状）：大小写混排、中日文、有无年份、有无 attention、有无封面、一个只剩缺失来源。
	specs := []groupedRelease{
		{title: "alpha", artist: "Zed", year: 2001, tracks: []string{"present"}},
		{title: "Alpha", artist: "Amy", year: nil, tracks: []string{"present", "present"}},
		{title: "Beta 专辑", artist: "艺术家", year: 1999, tracks: []string{"present"}, uncertain: 2},
		{title: "Gamma", artist: "Amy", year: 2020, tracks: []string{"present", "missing"}},
		{title: "delta", artist: "amy", year: 2005, tracks: []string{"present"}},
		{title: "Epsilon", artist: "Query Artist", year: 2010, tracks: []string{"present"}},
		{title: "Hidden Shell", artist: "Nobody", year: 2000, tracks: []string{"missing"}, uncertain: 1},
	}
	releaseIDs := map[string]string{}
	for _, spec := range specs {
		id, _ := insertGroupedRelease(t, application, rootID, spec)
		releaseIDs[spec.title] = id
	}
	if _, err := application.database.Exec(`INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
		VALUES($1::uuid,$2,'image/webp',640,480,'mirror-cover.webp','embedded')`, releaseIDs["Gamma"], strings.Repeat("c", 64)); err != nil {
		t.Fatalf("insert artwork fixture: %v", err)
	}
	groupOf := map[string]string{}
	rows, err := application.database.Query(`SELECT id::text,group_id::text FROM releases`)
	if err != nil {
		t.Fatalf("read group ids: %v", err)
	}
	for rows.Next() {
		var releaseID, groupID string
		if err := rows.Scan(&releaseID, &groupID); err != nil {
			t.Fatalf("scan group id: %v", err)
		}
		groupOf[releaseID] = groupID
	}
	rows.Close()

	for _, query := range []string{
		"",
		"?q=alpha",
		"?q=AMY",
		"?q=%E4%B8%93%E8%BE%91",     // 专辑
		"?q=Gamma%20track",          // 命中曲目标题
		"?q=no-such-release",        // 空结果
		"?attention=required",       // attention：不确定字段与缺失来源
		"?attention=required&q=amy", // 两个筛选叠加
		"?page=1&page_size=2",
		"?page=2&page_size=2",
		"?page=3&page_size=2",
		"?page=4&page_size=2", // 超出范围的页：空
		"?q=a&page=2&page_size=2",
	} {
		t.Run("query "+query, func(t *testing.T) {
			var releases releaseListResponse
			var groups releaseGroupListResponse
			getJSON(t, application, &user, "/api/v1/releases"+query, &releases)
			getJSON(t, application, &user, "/api/v1/release-groups"+query, &groups)
			if groups.Pagination != releases.Pagination {
				t.Fatalf("分页 = %+v，期望与 /releases 一致 %+v", groups.Pagination, releases.Pagination)
			}
			if len(groups.Items) != len(releases.Items) {
				t.Fatalf("按组返回 %d 项，/releases 返回 %d 项", len(groups.Items), len(releases.Items))
			}
			for index, group := range groups.Items {
				release := releases.Items[index]
				if !reflect.DeepEqual(group.Representative, release) {
					t.Fatalf("第 %d 项代表版本 = %+v，期望即 /releases 的 %+v", index, group.Representative, release)
				}
				if group.ID != groupOf[release.ID] || group.ReleaseCount != 1 || group.AttentionCount != release.AttentionCount {
					t.Fatalf("第 %d 项 = {id:%s count:%d attention:%d}，期望 {id:%s count:1 attention:%d}", index, group.ID, group.ReleaseCount, group.AttentionCount, groupOf[release.ID], release.AttentionCount)
				}
			}
		})
	}

	var all releaseGroupListResponse
	getJSON(t, application, &user, "/api/v1/release-groups", &all)
	if all.Pagination.Total != 6 {
		t.Fatalf("可见的组数 = %d，期望 6（只有缺失来源的 Release 不可见）", all.Pagination.Total)
	}
	body := application.request(t, http.MethodGet, "/api/v1/release-groups", nil, &user, nil).Body.String()
	if strings.Contains(body, releaseIDs["Hidden Shell"]) || !strings.Contains(body, `"artwork":null`) || !strings.Contains(body, `"resource_id":"mirror-cover.webp"`) {
		t.Fatalf("按组列表没有按摘要合同投影封面或泄露了不可见的 Release：%s", body)
	}
}

func TestPostgreSQLReleaseGroupRepresentativeAndCounts(t *testing.T) {
	application := newIntegrationTestApplication(t)
	user := application.createUser(t, "user")
	rootID := uuid.New()
	if _, err := application.database.Exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, application.allowedRoot); err != nil {
		t.Fatalf("insert root fixture: %v", err)
	}

	// 组 A：年份最早者是代表版本，没有年份的排最后；不可见成员不计入成员数与 attention。
	later, groupA := insertGroupedRelease(t, application, rootID, groupedRelease{title: "Later Reissue", artist: "Artist", year: 2010, createdAt: "2026-01-03T00:00:00Z", tracks: []string{"present", "present"}})
	original, _ := insertGroupedRelease(t, application, rootID, groupedRelease{groupID: groupA, title: "Original Pressing", artist: "Artist", year: 1999, createdAt: "2026-01-05T00:00:00Z", tracks: []string{"present", "missing"}, uncertain: 2})
	undated, _ := insertGroupedRelease(t, application, rootID, groupedRelease{groupID: groupA, title: "Undated Edition", artist: "Artist", year: nil, createdAt: "2026-01-01T00:00:00Z", tracks: []string{"present"}})
	hidden, _ := insertGroupedRelease(t, application, rootID, groupedRelease{groupID: groupA, title: "Hidden Edition", artist: "Artist", year: 1990, createdAt: "2025-12-01T00:00:00Z", tracks: []string{"missing"}, uncertain: 2})
	if _, err := application.database.Exec(`INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type) VALUES($1::uuid,$2,'image/png',300,300,'original-cover.png','embedded')`, original, strings.Repeat("d", 64)); err != nil {
		t.Fatalf("insert artwork fixture: %v", err)
	}

	// 组 B：同年份时创建更早者是代表版本。
	_, groupB := insertGroupedRelease(t, application, rootID, groupedRelease{title: "Tie Later", artist: "Artist", year: 2000, createdAt: "2026-02-02T00:00:00Z", tracks: []string{"present"}})
	earlier, _ := insertGroupedRelease(t, application, rootID, groupedRelease{groupID: groupB, title: "Tie Earlier", artist: "Artist", year: 2000, createdAt: "2026-02-01T00:00:00Z", tracks: []string{"present"}})

	// 组 C：年份与创建时间都相同时按 ID。
	smallerID, groupC := insertGroupedRelease(t, application, rootID, groupedRelease{id: "00000000-0000-4000-8000-000000000001", title: "Id Second", artist: "Artist", year: 2000, createdAt: "2026-03-01T00:00:00Z", tracks: []string{"present"}})
	insertGroupedRelease(t, application, rootID, groupedRelease{id: "00000000-0000-4000-8000-000000000002", groupID: groupC, title: "Id First", artist: "Artist", year: 2000, createdAt: "2026-03-01T00:00:00Z", tracks: []string{"present"}})

	// 组 D：都没有年份时按创建时间。
	oldest, groupD := insertGroupedRelease(t, application, rootID, groupedRelease{title: "Undated Oldest", artist: "Artist", year: nil, createdAt: "2026-04-01T00:00:00Z", tracks: []string{"present"}})
	insertGroupedRelease(t, application, rootID, groupedRelease{groupID: groupD, title: "Undated Newest", artist: "Artist", year: nil, createdAt: "2026-04-09T00:00:00Z", tracks: []string{"present"}})

	// 组 E：attention 只来自非代表版本；组 F：attention 只来自不可见成员。
	_, groupE := insertGroupedRelease(t, application, rootID, groupedRelease{title: "Calm Representative", artist: "Artist", year: 1990, tracks: []string{"present"}})
	insertGroupedRelease(t, application, rootID, groupedRelease{groupID: groupE, title: "Uncertain Member", artist: "Artist", year: 2005, tracks: []string{"present"}, uncertain: 2})
	_, groupF := insertGroupedRelease(t, application, rootID, groupedRelease{title: "Quiet Group", artist: "Artist", year: 1990, tracks: []string{"present"}})
	insertGroupedRelease(t, application, rootID, groupedRelease{groupID: groupF, title: "Noisy Hidden Member", artist: "Artist", year: 2005, tracks: []string{"missing"}, uncertain: 3})

	var all releaseGroupListResponse
	getJSON(t, application, &user, "/api/v1/release-groups?page_size=100", &all)
	byID := map[string]catalog.ReleaseGroupSummary{}
	for _, group := range all.Items {
		byID[group.ID] = group
	}
	for _, expectation := range []struct {
		name           string
		groupID        string
		representative string
		releaseCount   int64
		attentionCount int64
	}{
		{"最早年份，无年份排最后，不可见成员不计", groupA, original, 3, 3},
		{"同年份取创建更早者", groupB, earlier, 2, 0},
		{"年份与创建时间都相同取较小的 ID", groupC, smallerID, 2, 0},
		{"都没有年份取创建更早者", groupD, oldest, 2, 0},
		{"attention 之和包含非代表版本", groupE, "", 2, 2},
		{"不可见成员的 attention 不计入", groupF, "", 1, 0},
	} {
		group, found := byID[expectation.groupID]
		if !found {
			t.Fatalf("%s：组 %s 不在列表中", expectation.name, expectation.groupID)
		}
		if expectation.representative != "" && group.Representative.ID != expectation.representative {
			t.Fatalf("%s：代表版本 = %q，期望 %q", expectation.name, group.Representative.ID, expectation.representative)
		}
		if group.ReleaseCount != expectation.releaseCount || group.AttentionCount != expectation.attentionCount {
			t.Fatalf("%s：release_count=%d attention_count=%d，期望 %d / %d", expectation.name, group.ReleaseCount, group.AttentionCount, expectation.releaseCount, expectation.attentionCount)
		}
	}
	if group := byID[groupA]; group.Representative.Artwork == nil || group.Representative.Artwork.ResourceID != "original-cover.png" {
		t.Fatalf("代表版本的封面没有随摘要返回：%+v", group.Representative.Artwork)
	}

	// 详情：成员按代表版本规则排序，release_count 只数可见成员。
	var detail catalog.ReleaseGroupDetail
	getJSON(t, application, &user, "/api/v1/release-groups/"+groupA, &detail)
	memberIDs := make([]string, 0, len(detail.Releases))
	for _, member := range detail.Releases {
		memberIDs = append(memberIDs, member.ID)
	}
	if want := []string{original, later, undated}; !reflect.DeepEqual(memberIDs, want) {
		t.Fatalf("成员顺序 = %v，期望 %v（1999、2010、无年份）", memberIDs, want)
	}
	if detail.ID != groupA || detail.ReleaseCount != 3 || detail.AttentionCount != 3 || detail.Representative.ID != original || strings.Contains(fmt.Sprint(memberIDs), hidden) {
		t.Fatalf("组详情 = %+v，成员 %v", detail.ReleaseGroupSummary, memberIDs)
	}
	if !reflect.DeepEqual(detail.Representative, detail.Releases[0]) {
		t.Fatalf("代表版本 %+v 应与第一个成员 %+v 相同", detail.Representative, detail.Releases[0])
	}

	// 搜索与 attention 都按“任一/全部可见成员”判断。
	titles := func(path string) map[string]bool {
		var result releaseGroupListResponse
		getJSON(t, application, &user, path, &result)
		found := map[string]bool{}
		for _, group := range result.Items {
			found[group.ID] = true
		}
		return found
	}
	if found := titles("/api/v1/release-groups?q=Undated%20Edition"); !found[groupA] || len(found) != 1 {
		t.Fatalf("命中非代表版本标题的组应出现：%v", found)
	}
	if found := titles("/api/v1/release-groups?q=Original%20Pressing%20track"); !found[groupA] {
		t.Fatalf("命中成员曲目标题的组应出现：%v", found)
	}
	if found := titles("/api/v1/release-groups?q=Hidden%20Edition"); len(found) != 0 {
		t.Fatalf("只命中不可见成员的搜索不应出现任何组：%v", found)
	}
	attention := titles("/api/v1/release-groups?attention=required&page_size=100")
	if !attention[groupA] || !attention[groupE] || attention[groupF] || attention[groupB] || len(attention) != 2 {
		t.Fatalf("attention 筛选结果 = %v，期望组 A 与组 E", attention)
	}
	var page releaseGroupListResponse
	getJSON(t, application, &user, "/api/v1/release-groups?page=2&page_size=2", &page)
	if page.Pagination.Total != 6 || len(page.Items) != 2 {
		t.Fatalf("分页 = %+v", page.Pagination)
	}

	// 成员数超过上限：详情只返回前 100 条，release_count 仍是完整数量。
	_, bigGroup := insertGroupedRelease(t, application, rootID, groupedRelease{title: "Big Group 000", artist: "Artist", year: 1900, tracks: []string{"present"}})
	if _, err := application.database.Exec(`WITH inserted AS (
			INSERT INTO releases(id,group_id,title,artist,year,created_at,source_root_id,relative_directory,candidate_anchor,candidate_kind)
			SELECT gen_random_uuid(),$1::uuid,'Big Group ' || LPAD(n::text,3,'0'),'Artist',1900+n,'2026-05-01T00:00:00Z',$2::uuid,'big','big-' || n,'ordinary_directory'
			FROM generate_series(1,100) n
			RETURNING id
		), media_rows AS (
			INSERT INTO media(id,release_id,position,title) SELECT gen_random_uuid(),id,1,'Medium' FROM inserted RETURNING id
		)
		INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity)
		SELECT gen_random_uuid(),id,1,'Track','Artist',1,$2::uuid,'big/' || id::text,'present',NOW(),'physical',$2::text || ':big:' || id::text FROM media_rows`, bigGroup, rootID); err != nil {
		t.Fatalf("insert oversized group: %v", err)
	}
	var big catalog.ReleaseGroupDetail
	getJSON(t, application, &user, "/api/v1/release-groups/"+bigGroup, &big)
	if big.ReleaseCount != 101 || len(big.Releases) != 100 {
		t.Fatalf("超限的组：release_count=%d 成员=%d，期望 101 与 100", big.ReleaseCount, len(big.Releases))
	}
	if big.Representative.Title != "Big Group 000" || big.Releases[0].ID != big.Representative.ID || big.Releases[99].Title != "Big Group 099" {
		t.Fatalf("超限的组成员顺序不符：代表 %q，最后一个 %q", big.Representative.Title, big.Releases[99].Title)
	}
}

func TestPostgreSQLReleaseGroupDetailErrors(t *testing.T) {
	application := newIntegrationTestApplication(t)
	user := application.createUser(t, "user")
	rootID := uuid.New()
	if _, err := application.database.Exec(`INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, rootID, application.allowedRoot); err != nil {
		t.Fatalf("insert root fixture: %v", err)
	}
	_, visibleGroup := insertGroupedRelease(t, application, rootID, groupedRelease{title: "Visible", artist: "Artist", year: 2000, tracks: []string{"present"}})
	_, hiddenGroup := insertGroupedRelease(t, application, rootID, groupedRelease{title: "Only Missing", artist: "Artist", year: 2000, tracks: []string{"missing"}})
	emptyGroup := uuid.New()
	if _, err := application.database.Exec(`INSERT INTO release_groups(id) VALUES($1::uuid)`, emptyGroup); err != nil {
		t.Fatalf("insert empty group: %v", err)
	}

	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/release-groups", nil, nil, nil), http.StatusUnauthorized, "unauthorized")
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/release-groups/"+visibleGroup, nil, nil, nil), http.StatusUnauthorized, "unauthorized")
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/release-groups/not-a-uuid", nil, &user, nil), http.StatusBadRequest, "invalid_id")
	unknown := application.request(t, http.MethodGet, "/api/v1/release-groups/"+uuid.New(), nil, &user, nil)
	requireAPIError(t, unknown, http.StatusNotFound, "not_found")
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/release-groups/"+hiddenGroup, nil, &user, nil), http.StatusNotFound, "not_found")
	requireAPIError(t, application.request(t, http.MethodGet, "/api/v1/release-groups/"+emptyGroup, nil, &user, nil), http.StatusNotFound, "not_found")
	if body := unknown.Body.String(); strings.Contains(body, "SELECT ") || strings.Contains(body, "release_groups") {
		t.Fatalf("404 泄露了内部信息：%s", body)
	}

	for _, testCase := range []struct {
		path, code string
	}{
		{"/api/v1/release-groups?page=0", "invalid_pagination"},
		{"/api/v1/release-groups?page_size=101", "invalid_pagination"},
		{"/api/v1/release-groups?q=" + strings.Repeat("x", 201), "invalid_query"},
		{"/api/v1/release-groups?attention=guessed", "invalid_attention"},
	} {
		requireAPIError(t, application.request(t, http.MethodGet, testCase.path, nil, &user, nil), http.StatusBadRequest, testCase.code)
	}

	var detail catalog.ReleaseGroupDetail
	getJSON(t, application, &user, "/api/v1/release-groups/"+visibleGroup, &detail)
	if detail.ID != visibleGroup || detail.ReleaseCount != 1 || len(detail.Releases) != 1 || detail.Representative.Title != "Visible" {
		t.Fatalf("可见组详情 = %+v", detail)
	}
}
