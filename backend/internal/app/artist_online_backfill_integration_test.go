package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 5秒是CATP的100–200ms工程目标乘25的race调度容忍，不是产品SLO。
// 扫描计时覆盖POST、真实catalog写入和terminal succeeded，不把202当作完成。
// 全规模100k轨性能验收仍由独立benchmark负责，本例只锁定20k未补profile导致的全库兜底回归。
func TestPostgreSQLArtistOnlineWritesDuringPendingBackfill(t *testing.T) {
	app := newIntegrationTestApplication(t)
	admin := app.createUser(t, "admin")
	seedArtistOnlineNames(t, app)
	syntheticRoot := searchRoot(t, app)
	mustExec(t, app, `UPDATE library_roots SET status='disabled' WHERE id=$1::uuid`, syntheticRoot)
	var correction searchReleaseFixture
	for i := 1; i <= 24; i++ {
		correction = insertSearchRelease(t, app, syntheticRoot, groupedRelease{
			id: fmt.Sprintf("b4000000-0000-4000-8000-%012d", i), title: fmt.Sprintf("Online Fixture %02d", i),
			artist: fmt.Sprintf("Regression Artist %05d", i), tracks: []string{"present"},
		})
	}
	scanRoot := uuid.New()
	music := filepath.Join(app.allowedRoot, "online-probe")
	writeSyntheticTrack(t, filepath.Join(music, "probe.flac"), 71,
		"TITLE=Online Scan Track", "ALBUM=Online Scan Album", "ARTIST=Regression Artist 00001",
		"ALBUMARTIST=Regression Artist 00001", "COMPOSER=Regression Artist 00002", "TRACKNUMBER=1")
	mustExec(t, app, `INSERT INTO library_roots(id,path,status,revision) VALUES($1::uuid,$2,'active',1)`, scanRoot, music)
	var profiles, names int
	if err := app.database.QueryRow(`SELECT (SELECT count(*) FROM artist_profiles),
 (SELECT count(*) FROM artist_names WHERE spelling LIKE 'Regression %')`).Scan(&profiles, &names); err != nil {
		t.Fatal(err)
	}
	if profiles != 0 || names != 24000 {
		t.Fatalf("夹具不得预填profiles或缩减名字规模: profiles=%d names=%d", profiles, names)
	}

	barrier, logger := newArtistOnlineBatchBarrier()
	workerCtx, stopWorker := context.WithTimeout(context.Background(), 120*time.Second)
	finished := make(chan struct{})
	var workerErr error
	go func() {
		defer close(finished)
		workerErr = maintenance.Run(workerCtx, app.database, logger, maintenance.RunOptions{RefreshArtistResolutions: personal.RefreshArtistResolution})
	}()
	t.Cleanup(func() {
		stopWorker()
		app.shutdown()
		barrier.open()
		select {
		case <-finished:
		case <-time.After(10 * time.Second):
			t.Error("后台worker未响应清理取消")
		}
	})
	select {
	case <-barrier.entered:
	case <-finished:
		t.Fatalf("worker未在已提交批次暂停: %v", workerErr)
	case <-time.After(30 * time.Second):
		t.Fatal("未到达已提交批次barrier")
	}
	requireArtistOnlinePending(t, app)

	// 与故障复现相同，先发起扫描；其全流程不能靠等全库profiles补齐才能完成。
	scanStarted := time.Now()
	scanCtx, cancelScan := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelScan()
	response := artistOnlineRequest(t, scanCtx, app, admin, http.MethodPost, "/api/v1/scans", `{}`)
	if response.Code != http.StatusAccepted {
		t.Fatalf("启动扫描: %d %s", response.Code, response.Body)
	}
	var scan struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &scan); err != nil {
		t.Fatal(err)
	}
	if scan.ID == "" {
		t.Fatal("扫描未返回ID")
	}
	if err := app.app.WaitForScans(scanCtx); err != nil {
		t.Fatalf("实际扫描写入超出5秒工程回归上限: elapsed=%s error=%v", time.Since(scanStarted), err)
	}
	var status, trackID, scanGroup string
	if err := app.database.QueryRowContext(scanCtx, `SELECT status FROM scan_runs WHERE id=$1::uuid`, scan.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		t.Fatalf("扫描终态不是succeeded: %s", status)
	}
	if err := app.database.QueryRowContext(scanCtx, `SELECT t.id::text,r.group_id::text FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id
 WHERE t.source_root_id=$1::uuid AND t.title='Online Scan Track' AND t.source_status='present'`, scanRoot).Scan(&trackID, &scanGroup); err != nil {
		t.Fatalf("扫描未写入真实catalog: %v", err)
	}
	scanElapsed := time.Since(scanStarted)
	if scanElapsed > 5*time.Second {
		t.Fatalf("扫描完成耗时%s > 5秒", scanElapsed)
	}
	cancelScan()
	t.Logf("扫描含catalog写入及succeeded: %s", scanElapsed)
	requireArtistOnlinePending(t, app)

	correctionStarted := time.Now()
	correctionCtx, cancelCorrection := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelCorrection()
	response = artistOnlineRequest(t, correctionCtx, app, admin, http.MethodPatch, "/api/v1/releases/"+correction.releaseID+"/corrections",
		`{"expected_revision":1,"changes":{"album_artist":{"mode":"set","value":"Online Corrected Artist"},"label":{"mode":"set","value":"Online Corrected Label"}}}`)
	correctionElapsed := time.Since(correctionStarted)
	if response.Code != http.StatusOK || correctionElapsed > 5*time.Second {
		t.Fatalf("M3写入未在5秒内成功: elapsed=%s status=%d body=%s", correctionElapsed, response.Code, response.Body)
	}
	cancelCorrection()
	t.Logf("真实M3 PATCH: %s", correctionElapsed)
	requireArtistOnlinePending(t, app)
	requireArtistOnlineGraph(t, app, correction.releaseID, trackID)
	requireSearchTracks(t, app, &admin, "Online Scan Track", trackID)
	// 整理期保持legacy字段范围；有效艺人名应立即可搜，厂牌在发布后再断言。
	requireSearchAlbums(t, app, &admin, "Online Corrected Artist", correction.groupID)

	// 在线成功时后台仍被barrier停在批间；现在才放行，不伪造ready或手填任何完成marker。
	select {
	case <-finished:
		t.Fatal("在线写入期间后台提前完成")
	default:
	}
	barrier.open()
	select {
	case <-finished:
		if workerErr != nil {
			t.Fatalf("后台续跑未完成: %v", workerErr)
		}
	case <-workerCtx.Done():
		t.Fatalf("后台续跑未发布: %v", workerCtx.Err())
	}
	state, err := catalog.NewStore(app.database).LoadCatalogState(context.Background())
	if err != nil || state.SearchMode != "normalized" || state.ArtistsState != "ready" {
		t.Fatalf("续跑未完整发布: %+v %v", state, err)
	}
	var gaps int
	if err := app.database.QueryRow(`SELECT count(*) FROM artists a LEFT JOIN artist_profiles p ON p.artist_id=a.id
 WHERE p.artist_id IS NULL OR p.applied_generation<>(SELECT target_generation FROM catalog_projection_state WHERE capability='artists')`).Scan(&gaps); err != nil {
		t.Fatal(err)
	}
	if gaps != 0 {
		t.Fatalf("发布时仍有%d个profile缺口", gaps)
	}
	requireArtistOnlineGraph(t, app, correction.releaseID, trackID)
	requireSearchTracks(t, app, &admin, "Online Scan Track", trackID)
	requireSearchAlbums(t, app, &admin, "Online Scan Album", scanGroup)
	requireSearchAlbums(t, app, &admin, "Online Corrected Label", correction.groupID)
	requireSearchAlbums(t, app, &admin, "Online Corrected Artist", correction.groupID)
	// 最后一个真实发行的署名/alias由后台生成，证明不是只发布空壳身份。
	original := searchRead[catalog.SearchArtistItem](t, app, &admin, searchURL("artists", "Regression Artist 00023"))
	alias := searchRead[catalog.SearchArtistItem](t, app, &admin, searchURL("artists", "Regression Alias 00023"))
	if len(original.Items) != 1 || len(alias.Items) != 1 || original.Items[0].Artist.ID != alias.Items[0].Artist.ID || !alias.Items[0].Artist.Available {
		t.Fatalf("续跑后别名没有指向真实参与身份: original=%+v alias=%+v", original.Items, alias.Items)
	}
}
