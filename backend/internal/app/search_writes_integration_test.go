package app

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func TestPostgreSQLSearchCorrectionsClearNullAndRevert(t *testing.T) {
	for _, mode := range []string{"legacy", "normalized"} {
		t.Run(mode, func(t *testing.T) {
			env := newCorrectionsEnvironment(t)
			app := env.application
			// 曲目有自己的标题/艺人，避免旧发行标题经曲目字段合法命中。
			mustExec(t, app, `UPDATE tracks SET title='独立曲目',artist='独立艺人' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1::uuid)`, env.releaseID)
			var trackID string
			if err := app.database.QueryRow(`SELECT t.id::text FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1::uuid`, env.releaseID).Scan(&trackID); err != nil {
				t.Fatal(err)
			}
			if mode == "normalized" {
				publishSearch(t, app)
			}
			patch := func(revision int, changes map[string]any) string {
				t.Helper()
				response := env.patch(t, &env.admin, env.releaseID, uuid.New(), correctionBody(revision, changes))
				if response.Code != 200 {
					t.Fatalf("修正：%d %s", response.Code, response.Body.String())
				}
				return decodeJSONMap(t, response)["operation_id"].(string)
			}
			revert := func(id string) {
				t.Helper()
				response := app.request(t, http.MethodPost, revertPath(id), nil, &env.admin, idempotencyHeader(uuid.New()))
				if response.Code != 200 {
					t.Fatalf("撤销修正：%d %s", response.Code, response.Body.String())
				}
			}
			requireSearchAlbums(t, app, &env.ordinary, "原始标题", env.groupID)
			patch(1, map[string]any{"title": setChange("修正Ｂｅｙｏｎｃé"), "album_artist": setChange("修正周杰倫"), "label": setChange("修正Étiquette"), "catalog_number": setChange("ＳＲＣＬ－９９９")})
			requireSearchAlbums(t, app, &env.ordinary, "原始标题")
			requireSearchTracks(t, app, &env.ordinary, "原始艺人")
			requireSearchAlbums(t, app, &env.ordinary, "修正Ｂｅｙｏｎｃé", env.groupID)
			requireSearchTracks(t, app, &env.ordinary, "修正周杰倫", trackID)
			if mode == "legacy" {
				// 未发布时不偷偷混入新字段；修正提交即时可通过旧标题谓词检索。
				requireSearchTracks(t, app, &env.ordinary, "修正Étiquette")
				publishSearch(t, app)
			}
			requireSearchAlbums(t, app, &env.ordinary, "修正beyonce", env.groupID)
			requireSearchTracks(t, app, &env.ordinary, "修正周杰伦", trackID)
			requireSearchTracks(t, app, &env.ordinary, "修正etiquette", trackID)
			requireSearchTracks(t, app, &env.ordinary, "srcl999", trackID)
			requireSearchTracks(t, app, &env.ordinary, "原始厂牌")
			nulled := patch(2, map[string]any{"label": nullChange, "catalog_number": nullChange})
			requireSearchTracks(t, app, &env.ordinary, "修正etiquette")
			requireSearchTracks(t, app, &env.ordinary, "srcl999")
			requireSearchTracks(t, app, &env.ordinary, "原始厂牌")
			revert(nulled)
			requireSearchTracks(t, app, &env.ordinary, "修正etiquette", trackID)
			requireSearchTracks(t, app, &env.ordinary, "srcl999", trackID)
			cleared := patch(4, map[string]any{"title": clearChange, "album_artist": clearChange, "label": clearChange, "catalog_number": clearChange})
			requireSearchAlbums(t, app, &env.ordinary, "原始标题", env.groupID)
			requireSearchTracks(t, app, &env.ordinary, "原始厂牌", trackID)
			for _, query := range []string{"修正beyonce", "修正周杰伦", "修正etiquette", "srcl999"} {
				requireSearchTracks(t, app, &env.ordinary, query)
			}
			revert(cleared)
			requireSearchAlbums(t, app, &env.ordinary, "修正beyonce", env.groupID)
			requireSearchTracks(t, app, &env.ordinary, "修正周杰伦", trackID)
			requireSearchTracks(t, app, &env.ordinary, "修正etiquette", trackID)
			requireSearchTracks(t, app, &env.ordinary, "srcl999", trackID)
			requireSearchTracks(t, app, &env.ordinary, "原始厂牌")
		})
	}
}

func TestPostgreSQLSearchClaimAndRevertRefreshBothReleases(t *testing.T) {
	env := newClaimEnvironment(t)
	app := env.application
	publishSearch(t, app)
	requireSearchAlbums(t, app, &env.ordinary, "我改过的 A")
	revision, sourceRevision, token := env.previewToken(t)
	response := env.claim(t, &env.admin, env.b, uuid.New(), claimBody(revision, sourceRevision, env.a, token))
	if response.Code != 200 {
		t.Fatalf("认领：%d %s", response.Code, response.Body.String())
	}
	operation := decodeJSONMap(t, response)["operation_id"].(string)
	var claimedGroup string
	if err := app.database.QueryRow(`SELECT group_id::text FROM releases WHERE id=$1::uuid`, env.b).Scan(&claimedGroup); err != nil {
		t.Fatal(err)
	}
	page := requireSearchAlbums(t, app, &env.ordinary, "我改过的 A", claimedGroup)
	if page.Items[0].MatchedReleaseID != env.b {
		t.Fatalf("认领后没有转向新发行：%+v", page.Items)
	}
	// 临时恢复来源可见性，暴露“只刷新目标而漏清来源”的派生错误。
	mustExec(t, app, `UPDATE tracks SET source_status='present' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1::uuid)`, env.a)
	tracks := requireSearchAlbums(t, app, &env.ordinary, "我改过的 A", claimedGroup)
	if tracks.Items[0].MatchedReleaseID != env.b {
		t.Fatal("认领后的来源仍命中已迁走修正")
	}
	var oldGroup string
	if err := app.database.QueryRow(`SELECT group_id::text FROM releases WHERE id=$1::uuid`, env.a).Scan(&oldGroup); err != nil {
		t.Fatal(err)
	}
	requireSearchAlbums(t, app, &env.ordinary, "Album A", oldGroup)
	mustExec(t, app, `UPDATE tracks SET source_status='missing' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1::uuid)`, env.a)
	reverted := app.request(t, http.MethodPost, revertPath(operation), nil, &env.admin, idempotencyHeader(uuid.New()))
	if reverted.Code != 200 {
		t.Fatalf("撤销认领：%d %s", reverted.Code, reverted.Body.String())
	}
	requireSearchAlbums(t, app, &env.ordinary, "我改过的 A")
	mustExec(t, app, `UPDATE tracks SET source_status='present' WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1::uuid)`, env.a)
	restored := requireSearchAlbums(t, app, &env.ordinary, "我改过的 A", env.groupA)
	if restored.Items[0].MatchedReleaseID != env.a {
		t.Fatalf("撤销未刷新来源修正：%+v", restored.Items)
	}
	var targetGroup string
	if err := app.database.QueryRow(`SELECT group_id::text FROM releases WHERE id=$1::uuid`, env.b).Scan(&targetGroup); err != nil {
		t.Fatal(err)
	}
	target := requireSearchAlbums(t, app, &env.ordinary, "Album B", targetGroup)
	if target.Items[0].MatchedReleaseID != env.b {
		t.Fatalf("撤销未刷新目标：%+v", target.Items)
	}
}

func TestPostgreSQLSearchRealScanRefreshesPublishedFieldsAndMissing(t *testing.T) {
	app := newIntegrationTestApplication(t)
	admin := app.createUser(t, "admin")
	user := app.createUser(t, "user")
	library := createLibraryDirectory(t, app, "search-synthetic")
	path := filepath.Join(library, "album", "01.flac")
	writeSyntheticTrack(t, path, 41, "TITLE=扫描曲目", "ARTIST=曲目藝人", "ALBUM=扫描專輯", "ALBUMARTIST=专辑艺人", "COMPOSER=初始作曲者", "LABEL=初始厂牌", "CATALOGNUMBER=SCAN-1234")
	// 保留独立来源，使删除目标曲目后仍能完整扫描并执行 missing 对账，而非触发空根保护。
	writeSyntheticTrack(t, filepath.Join(library, "keeper", "01.flac"), 42, "TITLE=保留音轨", "ARTIST=独立保留者", "ALBUM=保留唱片", "ALBUMARTIST=独立保留者")
	response := app.request(t, http.MethodPost, "/api/v1/library-roots", map[string]string{"path": library}, &admin, idempotencyHeader(uuid.New()))
	if response.Code != 201 {
		t.Fatalf("注册合成库：%d %s", response.Code, response.Body.String())
	}
	// 先发布空库，再扫描：新增 UUID 不在初始工作清单内，必须由实时写者维护。
	publishSearch(t, app)
	assertScanFeedbackCounts(t, scanFeedbackReport(t, app, &admin, startScanFeedback(t, app, &admin)), [3]int{2, 0, 0}, [3]int{2, 0, 0})
	var trackID, releaseID, groupID string
	if err := app.database.QueryRow(`SELECT t.id::text,r.id::text,r.group_id::text FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id WHERE t.source_status='present' AND t.relative_path='album/01.flac'`).Scan(&trackID, &releaseID, &groupID); err != nil {
		t.Fatal(err)
	}
	requireSearchAlbums(t, app, &user, "扫描专辑", groupID)
	for _, query := range []string{"曲目艺人", "初始作曲者", "初始厂牌", "scan1234"} {
		requireSearchTracks(t, app, &user, query, trackID)
	}
	corrected := app.request(t, http.MethodPatch, "/api/v1/releases/"+releaseID+"/corrections", correctionBody(1, map[string]any{"label": setChange("人工厂牌")}), &admin, idempotencyHeader(uuid.New()))
	if corrected.Code != 200 {
		t.Fatalf("设置覆盖层：%d %s", corrected.Code, corrected.Body.String())
	}
	writeSyntheticTrack(t, path, 41, "TITLE=扫描新曲目", "ARTIST=新曲目艺人", "ALBUM=扫描新专辑", "ALBUMARTIST=专辑艺人", "COMPOSER=新作曲者", "LABEL=扫描新厂牌", "CATALOGNUMBER=NEW-5678")
	assertScanFeedbackCounts(t, scanFeedbackReport(t, app, &admin, startScanFeedback(t, app, &admin)), [3]int{0, 1, 0}, [3]int{0, 1, 0})
	for _, query := range []string{"扫描新曲目", "新曲目艺人", "新作曲者", "人工厂牌", "new5678"} {
		requireSearchTracks(t, app, &user, query, trackID)
	}
	for _, query := range []string{"初始作曲者", "初始厂牌", "scan1234", "扫描新厂牌"} {
		requireSearchTracks(t, app, &user, query)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	assertScanFeedbackCounts(t, scanFeedbackReport(t, app, &admin, startScanFeedback(t, app, &admin)), [3]int{0, 0, 1}, [3]int{0, 0, 1})
	requireSearchTracks(t, app, &user, "新作曲者")
	requireSearchAlbums(t, app, &user, "人工厂牌")
}
