package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 认领的真实重扫（阶段 M4 第三项第 1 步，只读）：用合成 FLAC 造出扫描新建发行的三种情形，
// 先用既有接口在旧版本上做好四类人工状态（修正、归入的专辑、默认版本、核对），再重扫，断言旧版本待认领、推荐命中新版本。
//
//   - 原地替换：同一路径换成另一份音频，旧曲目标为缺失并失去来源身份，新曲目在同一路径上。
//   - 新增文件：2 首加 3 首，原有曲目整体改挂到新发行，旧发行名下一首曲目都不剩，只有归组依据还记得原来的文件清单。
//   - 目录不变、文件整体换了名字和内容：新旧发行同目录，没有相同的文件。
//
// 单纯的文件改名（内容不变）不会新建发行：扫描按内容指纹把曲目重绑定到新路径，发行沿用，所以不属于认领。

// claimRescanEnvironment 是一个管理员加一个已登记并扫描过的素材目录：三张专辑各两首曲目。
type claimRescanEnvironment struct {
	application *integrationTestApplication
	admin       integrationTestUser
	library     string
	store       *catalog.Store
}

func newClaimRescanEnvironment(t *testing.T, albums ...string) *claimRescanEnvironment {
	t.Helper()
	application := newIntegrationTestApplication(t)
	environment := &claimRescanEnvironment{application: application, admin: application.createUser(t, "admin"), store: catalog.NewStore(application.database)}
	environment.library = createLibraryDirectory(t, application, "classical")
	for index, album := range albums {
		for number := 1; number <= 2; number++ {
			environment.write(t, album, number, byte(10*(index+1)+number), "Old")
		}
	}
	if response := application.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": environment.library}, &environment.admin, idempotencyHeader("create-classical")); response.Code != http.StatusCreated {
		t.Fatalf("register the library: HTTP %d: %s", response.Code, response.Body.String())
	}
	runScan(t, application, &environment.admin)
	return environment
}

func trackName(number int) string { return "0" + string(rune('0'+number)) + ".flac" }

// write 写一首合成曲目：md5 决定音频内容，标题前缀区分新旧。
func (environment *claimRescanEnvironment) write(t *testing.T, album string, number int, md5 byte, prefix string) {
	t.Helper()
	writeSyntheticTrack(t, filepath.Join(environment.library, album, trackName(number)), md5,
		"TITLE="+prefix+" "+album+" "+trackName(number), "ARTIST=Pianist", "ALBUM="+album, "TRACKNUMBER="+string(rune('0'+number)))
}

// releaseIDs 返回这个目录下的发行：先是现在可见的，再是不可见的（旧的）。每种情形只会有一个。
func (environment *claimRescanEnvironment) releaseIDs(t *testing.T, directory string) (visible, hidden string) {
	t.Helper()
	rows, err := environment.application.database.Query(`SELECT releases.id::text, EXISTS (
			SELECT 1 FROM media JOIN tracks ON tracks.medium_id=media.id WHERE media.release_id=releases.id AND tracks.source_status='present')
		FROM releases WHERE relative_directory=$1 ORDER BY created_at`, directory)
	if err != nil {
		t.Fatalf("read the releases of %s: %v", directory, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var isVisible bool
		if err := rows.Scan(&id, &isVisible); err != nil {
			t.Fatal(err)
		}
		if isVisible {
			visible = id
		} else {
			hidden = id
		}
	}
	return visible, hidden
}

func (environment *claimRescanEnvironment) revisionOf(t *testing.T, releaseID string) int {
	t.Helper()
	return int(decodeJSONMap(t, environment.application.request(t, http.MethodGet, "/api/v1/releases/"+releaseID, nil, &environment.admin, nil))["revision"].(float64))
}

// correct 用接口给版本设一个标题修正。
func (environment *claimRescanEnvironment) correct(t *testing.T, releaseID, title string) {
	t.Helper()
	response := environment.application.request(t, http.MethodPatch, "/api/v1/releases/"+releaseID+"/corrections",
		correctionBody(environment.revisionOf(t, releaseID), map[string]any{"title": setChange(title)}), &environment.admin, idempotencyHeader(uuid.New()))
	if response.Code != http.StatusOK {
		t.Fatalf("correct %s: HTTP %d: %s", releaseID, response.Code, response.Body.String())
	}
}

func TestPostgreSQLClaimCluesSurviveARealRescan(t *testing.T) {
	environment := newClaimRescanEnvironment(t, "replaced", "grown", "renamed")
	application := environment.application

	// 旧版本上的人工状态用接口做：三张专辑各设一个标题修正。归组、默认版本与核对的真实流程在后续步骤的认领用例里做；
	// 这里关心的是重扫之后留下的线索。
	replacedOld, _ := environment.releaseIDs(t, "replaced")
	grownOld, _ := environment.releaseIDs(t, "grown")
	renamedOld, _ := environment.releaseIDs(t, "renamed")
	for directory, id := range map[string]string{"replaced": replacedOld, "grown": grownOld, "renamed": renamedOld} {
		environment.correct(t, id, "我改过的 "+directory)
	}

	// 原地替换：同一路径换成另一份音频。
	for number := 1; number <= 2; number++ {
		environment.write(t, "replaced", number, byte(100+number), "New")
	}
	// 新增文件：2 首加 3 首。
	for number := 3; number <= 5; number++ {
		environment.write(t, "grown", number, byte(40+number), "Added")
	}
	// 目录不变，文件名与内容都换了。
	for number := 1; number <= 2; number++ {
		if err := os.Remove(filepath.Join(environment.library, "renamed", trackName(number))); err != nil {
			t.Fatalf("remove an old track: %v", err)
		}
		writeSyntheticTrack(t, filepath.Join(environment.library, "renamed", "rip-"+trackName(number)), byte(200+number),
			"TITLE=Rip "+trackName(number), "ARTIST=Pianist", "ALBUM=renamed", "TRACKNUMBER="+string(rune('0'+number)))
	}
	operationsBefore := countRows(t, application, `SELECT COUNT(*) FROM operations`)
	correctionsBefore := countRows(t, application, `SELECT COUNT(*) FROM release_corrections`)
	runScan(t, application, &environment.admin)

	for _, testCase := range []struct {
		directory, oldID string
		reasons          []string
		sameFiles        int64
	}{
		{"replaced", replacedOld, []string{catalog.ClaimReasonSameFiles, catalog.ClaimReasonSameDirectory}, 2},
		{"grown", grownOld, []string{catalog.ClaimReasonSameFiles, catalog.ClaimReasonSameDirectory}, 2},
		{"renamed", renamedOld, []string{catalog.ClaimReasonSameDirectory}, 0},
	} {
		t.Run(testCase.directory, func(t *testing.T) {
			newID, hiddenID := environment.releaseIDs(t, testCase.directory)
			if hiddenID != testCase.oldID || newID == "" || newID == testCase.oldID {
				t.Fatalf("after the rescan: visible %q, hidden %q; want a new visible release and the old %q hidden", newID, hiddenID, testCase.oldID)
			}
			suggestions, err := environment.store.LoadClaimSuggestions(context.Background(), testCase.oldID)
			if err != nil {
				t.Fatalf("load suggestions: %v", err)
			}
			if len(suggestions) != 1 || suggestions[0].Release.ID != newID {
				t.Fatalf("suggestions = %+v, want only the new release %s", suggestions, newID)
			}
			if !reflect.DeepEqual(suggestions[0].Reasons, testCase.reasons) || suggestions[0].SameFiles != testCase.sameFiles {
				t.Errorf("suggestion = reasons %v, same_files %d; want %v, %d", suggestions[0].Reasons, suggestions[0].SameFiles, testCase.reasons, testCase.sameFiles)
			}
		})
	}

	// 扫描不改人工状态与操作记录：旧版本上的修正还在，仍然只有那三条。
	if countRows(t, application, `SELECT COUNT(*) FROM operations`) != operationsBefore || countRows(t, application, `SELECT COUNT(*) FROM release_corrections`) != correctionsBefore {
		t.Fatal("the rescan changed the operation records or the corrections")
	}
	for _, id := range []string{replacedOld, grownOld, renamedOld} {
		if countRows(t, application, `SELECT COUNT(*) FROM release_corrections WHERE release_id=$1::uuid`, id) != 1 {
			t.Errorf("the old release %s lost its correction in the rescan", id)
		}
	}
}

// claimFlow 是一个管理员加两张各自成发行的合成专辑 a、b（每张三首，MEDIA 标签混合，所以介质类型拿不准）：真实流程的素材。
type claimFlow struct {
	*claimRescanEnvironment
}

func newClaimFlow(t *testing.T) *claimFlow {
	t.Helper()
	application := newIntegrationTestApplication(t)
	environment := &claimRescanEnvironment{application: application, admin: application.createUser(t, "admin"), store: catalog.NewStore(application.database)}
	environment.library = createLibraryDirectory(t, application, "classical")
	flow := &claimFlow{environment}
	flow.writeSet(t, "a", 10, "Old")
	flow.writeSet(t, "b", 30, "Old")
	if response := application.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": environment.library}, &environment.admin, idempotencyHeader("create-classical")); response.Code != http.StatusCreated {
		t.Fatalf("register the library: HTTP %d: %s", response.Code, response.Body.String())
	}
	runScan(t, application, &environment.admin)
	return flow
}

// writeSet 写一个目录的三首曲目：md5 的基数和标题前缀区分新旧；MEDIA 标签是 CD、Vinyl、CD，所以介质类型各曲目不一致。
func (flow *claimFlow) writeSet(t *testing.T, directory string, base byte, prefix string) {
	t.Helper()
	media := []string{"CD", "Vinyl", "CD"}
	for number := 1; number <= 3; number++ {
		writeSyntheticTrack(t, filepath.Join(flow.library, directory, trackName(number)), base+byte(number),
			"TITLE="+prefix+" "+string(rune('0'+number)), "ARTIST=Pianist", "ALBUM=Suite "+directory, "MEDIA="+media[number-1], "TRACKNUMBER="+string(rune('0'+number)))
	}
}

func (flow *claimFlow) get(t *testing.T, path string) map[string]any {
	t.Helper()
	response := flow.application.request(t, http.MethodGet, path, nil, &flow.admin, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d: %s", path, response.Code, response.Body.String())
	}
	return decodeJSONMap(t, response)
}

func (flow *claimFlow) post(t *testing.T, path string, body any) map[string]any {
	t.Helper()
	response := flow.application.request(t, http.MethodPost, path, body, &flow.admin, idempotencyHeader(uuid.New()))
	if response.Code != http.StatusOK {
		t.Fatalf("POST %s: HTTP %d: %s", path, response.Code, response.Body.String())
	}
	return decodeJSONMap(t, response)
}

// mediaTypeBasis 返回版本的介质类型问题的依据令牌（详情里给页面的那个）。
func (flow *claimFlow) mediaTypeBasis(t *testing.T, releaseID string) string {
	t.Helper()
	for _, field := range flow.get(t, "/api/v1/releases/"+releaseID+"/issues")["fields"].([]any) {
		if field.(map[string]any)["field"] == "media_type" && field.(map[string]any)["basis"] != nil {
			return field.(map[string]any)["basis"].(string)
		}
	}
	t.Fatalf("release %s has no reviewable media type issue", releaseID)
	return ""
}

// groupOf 读取版本现在所在的组（发行详情不含组，页面从按组读取知道归属；这里直接读库）。
func (flow *claimFlow) groupOf(t *testing.T, releaseID string) string {
	t.Helper()
	var groupID string
	if err := flow.application.database.QueryRow(`SELECT group_id::text FROM releases WHERE id=$1::uuid`, releaseID).Scan(&groupID); err != nil {
		t.Fatalf("read the group of %s: %v", releaseID, err)
	}
	return groupID
}

// members 返回一张专辑里可见成员的 ID。
func (flow *claimFlow) members(t *testing.T, groupID string) []string {
	t.Helper()
	ids := []string{}
	for _, member := range flow.get(t, "/api/v1/release-groups/"+groupID)["releases"].([]any) {
		ids = append(ids, member.(map[string]any)["id"].(string))
	}
	return ids
}

func (flow *claimFlow) attention(t *testing.T, releaseID string) int {
	t.Helper()
	return int(flow.get(t, "/api/v1/releases/"+releaseID)["attention_count"].(float64))
}

// 真实重扫的全流程（A9）：在旧版本 a 上做好四类人工状态（修正、归入 b 的专辑、设为默认版本、核对介质类型），
// 把 a 整张换成另一份音频并重扫，旧版本出现在待认领、推荐命中新版本、专辑详情提示它；认领之后新版本拥有四类状态，
// 原样再扫一遍它们保持（核对的依据相同，仍然有效）、扫描不改操作记录；撤销认领把四类状态还给旧版本。
func TestPostgreSQLClaimSurvivesRealRescans(t *testing.T) {
	flow := newClaimFlow(t)
	application := flow.application
	oldA, _ := flow.releaseIDs(t, "a")
	b, _ := flow.releaseIDs(t, "b")
	groupB := flow.groupOf(t, b)
	if flow.attention(t, oldA) != 1 {
		t.Fatalf("attention of a = %d, want 1 (the media type)", flow.attention(t, oldA))
	}

	// 四类人工状态都用接口做在 a 上。
	flow.correct(t, oldA, "我改过的 Suite a")
	flow.post(t, "/api/v1/releases/"+oldA+"/group", joinBody(flow.revisionOf(t, oldA), groupB))
	flow.post(t, "/api/v1/release-groups/"+groupB+"/default", defaultBody(int64(flow.get(t, "/api/v1/release-groups/"+groupB)["revision"].(float64)), oldA))
	flow.post(t, issueReviewPath(oldA), reviewBody(int64(flow.revisionOf(t, oldA)), "media_type", flow.mediaTypeBasis(t, oldA)))
	if flow.attention(t, oldA) != 0 {
		t.Fatalf("attention of a after the review = %d, want 0", flow.attention(t, oldA))
	}

	// 整张换成另一份音频（同路径同标签，音频不同），重扫：扫描证明不了是同一个版本，新建发行。
	flow.writeSet(t, "a", 100, "New")
	for number := 1; number <= 3; number++ {
		future := time.Now().Add(time.Minute)
		if err := os.Chtimes(filepath.Join(flow.library, "a", trackName(number)), future, future); err != nil {
			t.Fatalf("change the modification time: %v", err)
		}
	}
	runScan(t, application, &flow.admin)
	newA, hiddenA := flow.releaseIDs(t, "a")
	if hiddenA != oldA || newA == "" || newA == oldA {
		t.Fatalf("after replacing the audio: visible %q, hidden %q; want a new visible release and the old %q hidden", newA, hiddenA, oldA)
	}

	// 旧版本出现在待认领，位置与留着的修改写明白；详情推荐新版本（同样的文件）；新版本的专辑详情提示旧版本。
	listing := flow.get(t, "/api/v1/release-claims")
	items := listing["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["release"].(map[string]any)["id"] != oldA {
		t.Fatalf("claims = %v", listing)
	}
	entry := items[0].(map[string]any)
	holds, album, location := entry["holds"].(map[string]any), entry["album"].(map[string]any), entry["location"].(map[string]any)
	if holds["album"] != true || holds["default"] != true || len(holds["corrections"].([]any)) != 1 || len(holds["reviews"].([]any)) != 1 ||
		album["id"] != groupB || album["title"] != "Suite b" || location["directory"] != "a" {
		t.Fatalf("claim entry = %v", entry)
	}
	suggestions := flow.get(t, "/api/v1/release-claims/"+oldA)["suggestions"].([]any)
	if len(suggestions) != 1 || suggestions[0].(map[string]any)["release"].(map[string]any)["id"] != newA ||
		suggestions[0].(map[string]any)["same_files"].(float64) != 3 {
		t.Fatalf("suggestions = %v", suggestions)
	}
	hints := flow.get(t, "/api/v1/releases/"+newA+"/claim-hints")["items"].([]any)
	if len(hints) != 1 || hints[0].(map[string]any)["id"] != oldA {
		t.Fatalf("hints = %v", hints)
	}

	// 预览：四类都迁移；核对在新版本上依据相同，立即有效。
	preview := flow.get(t, claimPreviewPath(newA, oldA))
	effects := map[string]string{}
	kinds := []string{}
	for _, item := range preview["items"].([]any) {
		entry := item.(map[string]any)
		kinds = append(kinds, entry["kind"].(string)+":"+entry["action"].(string))
		if entry["kind"] == "review" {
			effects[entry["issue"].(string)] = entry["effect"].(string)
		}
	}
	if !reflect.DeepEqual(kinds, []string{"correction:move", "album:move", "default:move", "review:move"}) || effects["media_type"] != "valid" {
		t.Fatalf("preview items = %v, effects = %v", kinds, effects)
	}
	if flow.attention(t, newA) != 1 {
		t.Fatalf("attention of the new release before the claim = %d, want 1", flow.attention(t, newA))
	}
	groupBeforeClaim := flow.groupOf(t, newA)
	claimed := flow.post(t, claimPath(newA), claimBody(int64(preview["revision"].(float64)), int64(preview["source_revision"].(float64)), oldA, preview["token"].(string)))
	if claimed["group_id"] != groupB || claimed["previous_group_id"] != groupBeforeClaim || groupBeforeClaim == groupB {
		t.Fatalf("the claim response = %v, want the new release in group %s (it was in %s)", claimed, groupB, groupBeforeClaim)
	}
	claimOperation := claimed["operation_id"].(string)

	assertClaimed := func(label string) {
		t.Helper()
		detail := flow.get(t, "/api/v1/releases/"+newA)
		group := flow.get(t, "/api/v1/release-groups/"+groupB)
		if detail["title"] != "我改过的 Suite a" || !slices.Contains(flow.members(t, groupB), newA) || group["default_release_id"] != newA || flow.attention(t, newA) != 0 {
			t.Fatalf("%s: title %v, members %v, default %v, attention %d; want the migrated state", label, detail["title"], flow.members(t, groupB), group["default_release_id"], flow.attention(t, newA))
		}
		if len(flow.get(t, "/api/v1/release-claims")["items"].([]any)) != 0 {
			t.Fatalf("%s: the old release is still listed as claimable", label)
		}
	}
	assertClaimed("after the claim")

	// 原样重扫：四类保持，核对仍然有效，扫描不改操作记录。
	operationsBefore := countRows(t, application, `SELECT COUNT(*) FROM operations`)
	runScan(t, application, &flow.admin)
	assertClaimed("after an unchanged rescan")
	if countRows(t, application, `SELECT COUNT(*) FROM operations`) != operationsBefore {
		t.Fatal("the rescan changed the operation records")
	}
	if got, _ := flow.releaseIDs(t, "a"); got != newA {
		t.Fatalf("the rescan changed the release: %s -> %s", newA, got)
	}

	// 撤销认领：四类状态回到旧版本，它又待认领，新版本回到认领之前。
	flow.post(t, "/api/v1/operations/"+claimOperation+"/revert", nil)
	detail := flow.get(t, "/api/v1/releases/"+newA)
	if detail["title"] == "我改过的 Suite a" || slices.Contains(flow.members(t, groupB), newA) || flow.attention(t, newA) != 1 {
		t.Fatalf("after the revert the new release shows title %v, the album members are %v, attention %d", detail["title"], flow.members(t, groupB), flow.attention(t, newA))
	}
	if flow.get(t, "/api/v1/release-groups/"+groupB)["default_release_id"] == newA {
		t.Fatal("the new release is still the default version after the revert")
	}
	// 旧版本又拿回了它的人工状态：待认领，专辑里仍是 b。
	if got := flow.get(t, "/api/v1/release-claims/"+oldA); got["holds"].(map[string]any)["default"] != true || got["holds"].(map[string]any)["album"] != true {
		t.Fatalf("the old release after the revert = %v", got["holds"])
	}
	if len(flow.get(t, "/api/v1/release-claims")["items"].([]any)) != 1 {
		t.Fatal("the old release is not claimable again after the revert")
	}
}
