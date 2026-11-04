package ingest

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/maintenance"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func searchFeedbackPublish(t *testing.T, db *sql.DB) searchindex.State {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	before := searchFeedbackState(t, db)
	if err := maintenance.Initialize(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := maintenance.Run(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	after := searchFeedbackState(t, db)
	if !after.Ready() || after.CatalogEpoch != before.CatalogEpoch+1 {
		t.Fatalf("发布状态错误：before=%+v after=%+v", before, after)
	}
	return after
}

func searchFeedbackState(t *testing.T, db *sql.DB) searchindex.State {
	t.Helper()
	state, err := searchindex.LoadState(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func searchFeedbackCount(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var count int64
	if err := db.QueryRow(query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func searchFeedbackQuery(t *testing.T, db *sql.DB, epoch int64, kind, query string, want int64) {
	t.Helper()
	got, err := catalog.NewStore(db).Search(context.Background(), catalog.SearchParams{Type: kind, Query: query, Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if got.Pagination.Total != want || got.CatalogState.SearchMode != "normalized" || got.CatalogState.Epoch != epoch {
		t.Fatalf("搜索 %s/%q：%+v，期望数量 %d、epoch %d", kind, query, got, want, epoch)
	}
}

func searchFeedbackRevision(t *testing.T, db *sql.DB, track string) int64 {
	t.Helper()
	var revision, applied, generation, target int64
	if err := db.QueryRow(`SELECT s.input_revision,s.search_applied_input_revision,s.search_applied_generation,p.target_generation
 FROM catalog_projection_subject_state s CROSS JOIN catalog_projection_state p
 WHERE s.subject_kind='track' AND s.subject_id=$1 AND p.capability='search'`, track).Scan(&revision, &applied, &generation, &target); err != nil {
		t.Fatal(err)
	}
	if revision != applied || generation != target {
		t.Fatalf("曲目投影过期：revision=%d applied=%d generation=%d target=%d", revision, applied, generation, target)
	}
	return revision
}

func searchFeedbackMissing(t *testing.T, db *sql.DB, f *ScanFeedback, root string) {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := MarkMissingSourcesWithFeedback(context.Background(), tx, f.scanID, []string{root}, f); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgreSQLSearchFeedbackSameScanChanges(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	initial := NewScanFeedback(feedbackRun(t, db))
	a, b := observation("album/a.flac", "舊曲", 1), observation("gone/b.flac", "消失曲", 1)
	b.Directory, b.Album = "gone", "消失專輯"
	release := feedbackPersist(t, db, initial, root, feedbackCandidate(t, db, initial.scanID, root, "", nil, a))
	missingRelease := feedbackPersist(t, db, initial, root, feedbackCandidate(t, db, initial.scanID, root, "", nil, b))
	track, missing := feedbackTracks(t, db, release)[0], feedbackTracks(t, db, missingRelease)[0]
	state := searchFeedbackPublish(t, db)
	revision, missingRevision := searchFeedbackRevision(t, db, track), searchFeedbackRevision(t, db, missing)
	// 同一个 scanID 同时包含新发行、已有曲目更新和整张专辑消失。
	f := NewScanFeedback(feedbackRun(t, db))
	a.Title = "更新樂曲"
	feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, release, []string{track}, a))
	c := observation("new/c.flac", "新增樂曲", 1)
	c.Directory, c.Album = "new", "新增專輯"
	added := feedbackPersist(t, db, f, root, feedbackCandidate(t, db, f.scanID, root, "", nil, c))
	searchFeedbackMissing(t, db, f, root)
	if f.Broken() {
		t.Fatal(f.Err())
	}
	want := ScanChangeSummary{Albums: ChangeCounts{Added: 1, Updated: 1, Missing: 1}, Tracks: ChangeCounts{Added: 1, Updated: 1, Missing: 1}}
	if got := feedbackSummary(t, db, f); got != want {
		t.Fatalf("同轮变化：%+v，期望 %+v", got, want)
	}
	if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM scan_added_releases WHERE scan_run_id=$1`, f.scanID); got != 1 {
		t.Fatalf("新增历史数：%d", got)
	}
	if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM scan_added_releases a JOIN releases r ON r.id=a.release_id
 WHERE a.scan_run_id=$1 AND a.release_id=$2 AND a.group_id_at_creation=r.group_id AND a.added_at=r.created_at
 AND a.title_at_creation=$3 AND a.artist_at_creation=r.artist`, f.scanID, added, c.Album); got != 1 {
		t.Fatal("新增历史与核心发行不一致")
	}
	if got := searchFeedbackRevision(t, db, track); got != revision+1 {
		t.Fatalf("更新 revision=%d", got)
	}
	if got := searchFeedbackRevision(t, db, missing); got != missingRevision+1 {
		t.Fatalf("missing revision=%d", got)
	}
	for _, kind := range []string{"tracks", "albums"} {
		searchFeedbackQuery(t, db, state.CatalogEpoch, kind, "更新乐曲", 1)
		searchFeedbackQuery(t, db, state.CatalogEpoch, kind, "新增专辑", 1)
		searchFeedbackQuery(t, db, state.CatalogEpoch, kind, "消失曲", 0)
		searchFeedbackQuery(t, db, state.CatalogEpoch, kind, "旧曲", 0)
	}
	if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM catalog_search_values WHERE track_id=$1 AND field='track_title' AND normalized_value='消失曲'`, missing); got != 1 {
		t.Fatal("missing 不应删除原搜索值")
	}
	// 扫描不是发布，不应按曲目或批次增加 epoch。
	if after := searchFeedbackState(t, db); !after.Ready() || after.CatalogEpoch != state.CatalogEpoch {
		t.Fatalf("扫描改变发布状态：%+v", after)
	}
}

func TestPostgreSQLSearchFeedbackCandidateProjectionFailureRollsBack(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, field := range []string{"track_title", "release_title"} {
			t.Run(fmt.Sprintf("existing=%t/%s", existing, field), func(t *testing.T) {
				db := feedbackDB(t)
				root := feedbackRoot(t, db)
				a := observation("album/a.flac", "原曲", 1)
				initial := NewScanFeedback(feedbackRun(t, db))
				release := feedbackPersist(t, db, initial, root, feedbackCandidate(t, db, initial.scanID, root, "", nil, a))
				track := feedbackTracks(t, db, release)[0]
				state := searchFeedbackPublish(t, db)
				revision := searchFeedbackRevision(t, db, track)
				tables := []string{"release_groups", "releases", "media", "tracks", "catalog_search_values", "catalog_projection_subject_state"}
				before := make(map[string]int64)
				for _, table := range tables {
					before[table] = searchFeedbackCount(t, db, `SELECT COUNT(*) FROM `+table)
				}
				feedbackExec(t, db, `CREATE FUNCTION reject_search_value() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
     IF NEW.field=TG_ARGV[0] THEN RAISE EXCEPTION 'search projection fault'; END IF; RETURN NEW; END $$`)
				// field 来自测试固定列表，触发器只用于故障注入。
				feedbackExec(t, db, `CREATE TRIGGER reject_search_value BEFORE INSERT ON catalog_search_values FOR EACH ROW EXECUTE FUNCTION reject_search_value('`+field+`')`)
				f := NewScanFeedback(feedbackRun(t, db))
				plannedRelease := ""
				var ids []string
				if existing {
					plannedRelease, ids = release, []string{track}
				} else {
					a.RelativePath = "new/a.flac"
				}
				a.Title, a.Album = "未提交曲", "未提交專輯"
				b := observation("new/b.flac", "未提交新曲", 2)
				b.Album = a.Album
				candidate := feedbackCandidate(t, db, f.scanID, root, plannedRelease, ids, a, b)
				if _, err := PersistCandidateWithFeedback(context.Background(), db, f.scanID, root, candidate, f); err == nil || !strings.Contains(err.Error(), "search projection fault") {
					t.Fatalf("投影故障被吞掉：%v", err)
				}
				if f.Broken() {
					t.Fatalf("核心故障误归类为反馈降级：%v", f.Err())
				}
				for _, table := range tables {
					if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM `+table); got != before[table] {
						t.Fatalf("%s 部分提交：%d -> %d", table, before[table], got)
					}
				}
				if got := searchFeedbackRevision(t, db, track); got != revision {
					t.Fatalf("回滚后 revision=%d", got)
				}
				if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM tracks WHERE id=$1 AND title='原曲' AND source_status='present'`, track); got != 1 {
					t.Fatal("原曲目事实未回滚")
				}
				if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM releases WHERE id=$1 AND title='Album'`, release); got != 1 {
					t.Fatal("原发行事实未回滚")
				}
				for _, table := range []string{"scan_catalog_changes", "scan_added_releases"} {
					if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM `+table+` WHERE scan_run_id=$1`, f.scanID); got != 0 {
						t.Fatalf("%s 留下失败候选记录", table)
					}
				}
				if got := searchFeedbackState(t, db); got != state {
					t.Fatalf("回滚后搜索状态变化：%+v", got)
				}
				searchFeedbackQuery(t, db, state.CatalogEpoch, "tracks", "原曲", 1)
				searchFeedbackQuery(t, db, state.CatalogEpoch, "tracks", "未提交", 0)
			})
		}
	}
}

func TestPostgreSQLSearchFeedbackFailurePreservesCandidateAndMissing(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	state := searchFeedbackPublish(t, db)
	feedbackExec(t, db, `CREATE FUNCTION reject_search_feedback() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'feedback fault'; END $$`)
	feedbackExec(t, db, `CREATE TRIGGER reject_search_feedback BEFORE INSERT ON scan_catalog_changes FOR EACH ROW EXECUTE FUNCTION reject_search_feedback()`)
	f := NewScanFeedback(feedbackRun(t, db))
	a := observation("album/a.flac", "繁體樂曲", 1)
	c := feedbackCandidate(t, db, f.scanID, root, "", nil, a)
	release, err := PersistCandidateWithFeedback(context.Background(), db, f.scanID, root, c, f)
	if err != nil {
		t.Fatal(err)
	}
	if !f.Broken() {
		t.Fatal("反馈故障未降级")
	}
	track := feedbackTracks(t, db, release)[0]
	revision := searchFeedbackRevision(t, db, track)
	searchFeedbackQuery(t, db, state.CatalogEpoch, "tracks", "繁体乐曲", 1)
	searchFeedbackQuery(t, db, state.CatalogEpoch, "albums", "繁体乐曲", 1)
	for _, table := range []string{"scan_catalog_changes", "scan_added_releases"} {
		if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM `+table+` WHERE scan_run_id=$1`, f.scanID); got != 0 {
			t.Fatalf("%s 留下半份反馈", table)
		}
	}
	// 已损坏的同一反馈对象仍须完成 missing 的核心投影更新。
	feedbackExec(t, db, `UPDATE tracks SET observed_at=(SELECT started_at-INTERVAL '1 second' FROM scan_runs WHERE id=$1) WHERE id=$2`, f.scanID, track)
	searchFeedbackMissing(t, db, f, root)
	if got := searchFeedbackRevision(t, db, track); got != revision+1 {
		t.Fatalf("已降级 missing 未刷新：%d", got)
	}
	searchFeedbackQuery(t, db, state.CatalogEpoch, "tracks", "繁体乐曲", 0)
	searchFeedbackQuery(t, db, state.CatalogEpoch, "albums", "繁体乐曲", 0)
}

func TestPostgreSQLSearchFeedbackMissingDegradationRefreshesProjection(t *testing.T) {
	for _, failure := range []string{"before", "after", "disabled"} {
		t.Run(failure, func(t *testing.T) {
			db := feedbackDB(t)
			root := feedbackRoot(t, db)
			initial := NewScanFeedback(feedbackRun(t, db))
			release := feedbackPersist(t, db, initial, root, feedbackCandidate(t, db, initial.scanID, root, "", nil, observation("album/a.flac", "繁體曲", 1)))
			track := feedbackTracks(t, db, release)[0]
			state := searchFeedbackPublish(t, db)
			revision := searchFeedbackRevision(t, db, track)
			f := NewScanFeedback(feedbackRun(t, db))
			scanID := f.scanID
			if failure == "after" {
				feedbackExec(t, db, `CREATE FUNCTION reject_missing_feedback() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'missing feedback fault'; END $$`)
				feedbackExec(t, db, `CREATE TRIGGER reject_missing_feedback BEFORE INSERT ON scan_catalog_changes FOR EACH ROW EXECUTE FUNCTION reject_missing_feedback()`)
			}
			tx, err := db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if failure == "before" {
				feedbackExec(t, tx, `CREATE TEMP TABLE scan_feedback_tracks(id uuid PRIMARY KEY) ON COMMIT DROP`)
			}
			if failure == "disabled" {
				f = nil
			}
			if err := MarkMissingSourcesWithFeedback(context.Background(), tx, scanID, []string{root}, f); err != nil {
				t.Fatal(err)
			}
			if failure != "disabled" && !f.Broken() {
				t.Fatal("missing 反馈故障未降级")
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if got := searchFeedbackRevision(t, db, track); got != revision+1 {
				t.Fatalf("反馈降级跳过搜索刷新：%d", got)
			}
			if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM scan_catalog_changes WHERE scan_run_id=$1`, scanID); got != 0 {
				t.Fatal("保留了半份反馈")
			}
			searchFeedbackQuery(t, db, state.CatalogEpoch, "tracks", "繁体曲", 0)
			searchFeedbackQuery(t, db, state.CatalogEpoch, "albums", "繁体曲", 0)
		})
	}
}

func TestPostgreSQLSearchFeedbackMissingBatchesProjectionFailureRollsBack(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	group, release, medium := uuid.New(), uuid.New(), uuid.New()
	feedbackExec(t, db, `INSERT INTO release_groups(id) VALUES($1)`, group)
	feedbackExec(t, db, `INSERT INTO releases(id,group_id,title,artist) VALUES($1,$2,'批次專輯','Artist')`, release, group)
	feedbackExec(t, db, `INSERT INTO media(id,release_id,position) VALUES($1,$2,1)`, medium, release)
	feedbackExec(t, db, `INSERT INTO tracks(id,medium_id,position,title,artist,source_root_id,relative_path,observed_at)
 SELECT gen_random_uuid(),$1,n,'批次曲','Artist',$2,n::text||'.flac',NOW()-INTERVAL '1 hour' FROM generate_series(1,600)n`, medium, root)
	state := searchFeedbackPublish(t, db)
	// 第 257 个 UUID 在第二批刷新时失败，证明第一批核心与投影也整体回滚。
	feedbackExec(t, db, `CREATE FUNCTION reject_second_search_batch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF NEW.subject_kind='track' AND NEW.subject_id=(SELECT id FROM tracks ORDER BY id OFFSET 256 LIMIT 1) THEN
  IF (SELECT COUNT(*) FROM tracks WHERE source_status='missing')<>512 THEN RAISE EXCEPTION 'unbounded missing batch'; END IF;
  RAISE EXCEPTION 'second batch projection fault';
 END IF; RETURN NEW; END $$`)
	feedbackExec(t, db, `CREATE TRIGGER reject_second_search_batch BEFORE UPDATE ON catalog_projection_subject_state FOR EACH ROW EXECUTE FUNCTION reject_second_search_batch()`)
	f := NewScanFeedback(feedbackRun(t, db))
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	err = MarkMissingSourcesWithFeedback(context.Background(), tx, f.scanID, []string{root}, f)
	if err == nil || !strings.Contains(err.Error(), "second batch projection fault") {
		t.Fatalf("第二批核心故障被吞掉或批次无界：%v", err)
	}
	if f.Broken() {
		t.Fatalf("搜索故障误降级为反馈：%v", f.Err())
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM tracks WHERE source_status='present'`); got != 600 {
		t.Fatalf("跨批核心未回滚：%d", got)
	}
	if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM catalog_projection_subject_state WHERE subject_kind='track' AND input_revision=0 AND search_applied_input_revision=0 AND search_applied_generation=$1`, state.TargetGeneration); got != 600 {
		t.Fatalf("跨批投影未回滚：%d", got)
	}
	if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM scan_catalog_changes WHERE scan_run_id=$1`, f.scanID); got != 0 {
		t.Fatal("失败 missing 留下反馈")
	}
	if got := searchFeedbackState(t, db); got != state {
		t.Fatalf("失败后发布状态改变：%+v", got)
	}
	searchFeedbackQuery(t, db, state.CatalogEpoch, "tracks", "批次曲", 600)
	feedbackExec(t, db, `DROP TRIGGER reject_second_search_batch ON catalog_projection_subject_state`)
	searchFeedbackMissing(t, db, f, root)
	if f.Broken() {
		t.Fatal(f.Err())
	}
	if got := searchFeedbackCount(t, db, `SELECT COUNT(*) FROM catalog_projection_subject_state WHERE subject_kind='track' AND input_revision=1 AND search_applied_input_revision=1 AND search_applied_generation=$1`, state.TargetGeneration); got != 600 {
		t.Fatalf("重试未刷新全部批次：%d", got)
	}
	if got := feedbackSummary(t, db, f); got != (ScanChangeSummary{Albums: ChangeCounts{Missing: 1}, Tracks: ChangeCounts{Missing: 600}}) {
		t.Fatalf("重试报告：%+v", got)
	}
	searchFeedbackQuery(t, db, state.CatalogEpoch, "tracks", "批次曲", 0)
}

func TestPostgreSQLSearchFeedbackMissingTakesGateBeforeBusinessLocks(t *testing.T) {
	db := feedbackDB(t)
	root := feedbackRoot(t, db)
	initial := NewScanFeedback(feedbackRun(t, db))
	release := feedbackPersist(t, db, initial, root, feedbackCandidate(t, db, initial.scanID, root, "", nil, observation("album/a.flac", "A", 1)))
	f := NewScanFeedback(feedbackRun(t, db))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gate, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback()
	if err := LockProjectionWrites(ctx, gate); err != nil {
		t.Fatal(err)
	}
	scan, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 先释放阻塞者，再等待扫描事务退出，避免失败路径清理时彼此等待。
	defer func() { _ = gate.Rollback(); _ = scan.Rollback() }()
	var pid int
	if err := scan.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- MarkMissingSourcesWithFeedback(ctx, scan, f.scanID, []string{root}, f) }()
	waitFeedbackLock(t, db, ctx, pid, "SELECT pg_advisory_xact_lock")
	probe, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Rollback()
	feedbackExec(t, probe, `SELECT id FROM releases WHERE id=$1 FOR UPDATE NOWAIT`, release)
	feedbackExec(t, probe, `SELECT id FROM tracks WHERE source_root_id=$1 FOR UPDATE NOWAIT`, root)
	if err := probe.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := gate.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if f.Broken() {
		t.Fatal(f.Err())
	}
	if err := scan.Commit(); err != nil {
		t.Fatal(err)
	}
}
