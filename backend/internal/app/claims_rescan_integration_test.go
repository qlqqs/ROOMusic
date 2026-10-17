package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

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
