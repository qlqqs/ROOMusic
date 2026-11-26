package library

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"time"

	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func directoryExec(t *testing.T, f *libraryFixture, query string, args ...any) {
	t.Helper()
	_, err := f.database.ExecContext(t.Context(), query, args...)
	directoryOK(t, err)
}

func directoryOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func startDirectoryScan(t *testing.T, s *Service, options StartOptions) string {
	t.Helper()
	result, err := s.StartScan(t.Context(), options)
	directoryOK(t, err)
	if !result.Started {
		t.Fatalf("扫描未启动：%+v", result)
	}
	return result.Scan.ID
}

func waitDirectoryScan(t *testing.T, s *Service, id, status string) Scan {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	directoryOK(t, s.WaitForScans(ctx))
	scan, err := s.GetScan(ctx, id)
	directoryOK(t, err)
	requireScanStatus(t, testScanResult{ID: scan.ID, Status: scan.Status}, status)
	return scan
}

func scopedDirectoryOptions(root registeredRoot, dirs, removedTrees []string) StartOptions {
	return StartOptions{Mode: ScanModeIncremental, Trigger: ScanTriggerScheduled, prepare: func(context.Context, *sql.Tx) (scanTargets, error) {
		return scanTargets{root.ID: {revision: root.revision, directories: dirs, removedTrees: removedTrees}}, nil
	}}
}

func writeDirectoryTrack(t *testing.T, root registeredRoot, path, album string, seed byte) {
	t.Helper()
	writeAudio(t, root, path, audioFile{seed: seed, title: filepath.Base(path), album: album, track: int(seed)})
}

func writeDirectoryCUE(t *testing.T, root registeredRoot) {
	t.Helper()
	writeFixtureFile(t, root, "media/image.flac", parsertest.FLACWithComments(t, []string{"TITLE=Image", "ARTIST=Artist", "ALBUM=Cue Album"}))
	writeFixtureFile(t, root, "sheets/album.cue", []byte(`TITLE "Cue Album"
PERFORMER "Artist"
FILE "../media/image.flac" FLAC
  TRACK 01 AUDIO
    TITLE "First"
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    TITLE "Second"
    INDEX 01 00:01:00
`))
}

func TestPostgreSQLDirectoryScanScope(t *testing.T) {
	t.Run("移动、相似目录与保留兄弟缓存", func(t *testing.T) {
		f := newLibraryFixture(t)
		s := scannerTestApplication(f)
		root := insertScannerRoot(t, f, "scope")
		root.revision = 1
		for i, path := range []string{"A%_/01.flac", "Axx/01.flac", "A%_-old/01.flac", "solo.flac", "A%_/Skip/01.flac", "Axx/duplicate.flac"} {
			seed := byte(i + 1)
			if i == 5 {
				seed = 1
			}
			writeDirectoryTrack(t, root, path, filepath.Dir(path), seed)
		}
		directoryExec(t, f, `INSERT INTO library_root_exclusions(root_id,relative_path) VALUES($1,'A%_/Skip')`, root.ID)
		boundaryCompletedScan(t, s)
		before := presentTrackIDs(t, f, root.ID)
		if len(before) != 5 || before["A%_/Skip/01.flac"] != "" {
			t.Fatalf("首次扫描应保留五首曲目且跳过排除树：%v", before)
		}

		// 兄弟目录中的同指纹副本也已从磁盘移除，但并未进入本轮扫描范围。
		directoryOK(t, os.Remove(filepath.Join(root.Path, "Axx/duplicate.flac")))
		directoryOK(t, os.Remove(filepath.Join(root.Path, "A%_-old/01.flac")))
		moveFixture(t, root, "A%_/01.flac", "New/01.flac")
		id := startDirectoryScan(t, s, scopedDirectoryOptions(root, []string{"A%_", "A%_/Skip", "New"}, nil))
		waitDirectoryScan(t, s, id, "succeeded")
		after := presentTrackIDs(t, f, root.ID)
		requireTrackAt(t, after, "New/01.flac", before["A%_/01.flac"])
		for _, path := range []string{"Axx/01.flac", "Axx/duplicate.flac", "A%_-old/01.flac", "solo.flac"} {
			requireTrackAt(t, after, path, before[path])
		}
		report, err := s.GetScanReport(t.Context(), id, 1, 20)
		directoryOK(t, err)
		if report.Files.Total == nil || *report.Files.Total != 1 || report.Files.Processed != 1 || report.Tracks.Missing != 0 {
			t.Errorf("局部移动的计数或缺失反馈越界：%+v", report)
		}
		assertDatabaseCount(t, f, `SELECT count(*) FROM library_parse_cache WHERE root_id=$1 AND relative_path IN ('Axx/01.flac','Axx/duplicate.flac','A%_-old/01.flac','solo.flac')`, 4, root.ID)

		directoryOK(t, os.Remove(filepath.Join(root.Path, "New/01.flac")))
		id = startDirectoryScan(t, s, scopedDirectoryOptions(root, []string{"New"}, nil))
		waitDirectoryScan(t, s, id, "succeeded")
		report, err = s.GetScanReport(t.Context(), id, 1, 20)
		directoryOK(t, err)
		if !report.MissingReconciled || report.Tracks.Missing != 1 || report.Files.Total == nil || *report.Files.Total != 0 {
			t.Errorf("可访问的空子目录应报告一首缺失：%+v", report)
		}
		assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE id=$1 AND source_status='missing'`, 1, before["A%_/01.flac"])
	})

	t.Run("多碟与跨目录 CUE", func(t *testing.T) {
		f := newLibraryFixture(t)
		s := scannerTestApplication(f)
		root := insertScannerRoot(t, f, "album-scope")
		root.revision = 1
		writeArtworkPriorityFLAC(t, root, "Box/CD1/01.flac", "Box", 1, 10, nil)
		writeArtworkPriorityFLAC(t, root, "Box/CD2/01.flac", "Box", 2, 11, nil)
		writeDirectoryCUE(t, root)
		writeDirectoryTrack(t, root, "Other/01.flac", "Other", 12)
		boundaryCompletedScan(t, s)
		before := presentTrackIDs(t, f, root.ID)
		const cueQuery = `SELECT count(*) FROM tracks WHERE source_root_id=$1 AND source_status='present'
			AND cue_sheet_path='sheets/album.cue' AND cue_parent_relative_path='media/image.flac'`
		assertDatabaseCount(t, f, cueQuery, 2, root.ID)
		for _, test := range []struct {
			dir   string
			files int64
		}{{"Box/CD2", 2}, {"media", 2}, {"sheets", 2}} {
			id := startDirectoryScan(t, s, scopedDirectoryOptions(root, []string{test.dir}, nil))
			waitDirectoryScan(t, s, id, "succeeded")
			report, err := s.GetScanReport(t.Context(), id, 1, 20)
			directoryOK(t, err)
			if report.Files.Total == nil || *report.Files.Total != test.files || report.Tracks.Missing != 0 {
				t.Errorf("%s 的范围预计 %d 个文件、零缺失，反馈 %+v", test.dir, test.files, report)
			}
			after := presentTrackIDs(t, f, root.ID)
			for path, trackID := range before {
				requireTrackAt(t, after, path, trackID)
			}
			assertDatabaseCount(t, f, cueQuery, 2, root.ID)
		}
	})

	t.Run("子树移入、改名与移出", func(t *testing.T) {
		f := newLibraryFixture(t)
		s := scannerTestApplication(f)
		root := insertScannerRoot(t, f, "tree-scope")
		root.revision = 1
		boundaryCompletedScan(t, s)
		staged := registeredRoot{Path: t.TempDir()}
		writeDirectoryTrack(t, staged, "Tree/Child/01.flac", "Tree", 16)
		directoryOK(t, os.Rename(filepath.Join(staged.Path, "Tree"), filepath.Join(root.Path, "Tree")))
		scan := func(dirs, removed []string) ScanReport {
			t.Helper()
			id := startDirectoryScan(t, s, scopedDirectoryOptions(root, dirs, removed))
			waitDirectoryScan(t, s, id, "succeeded")
			report, err := s.GetScanReport(t.Context(), id, 1, 20)
			directoryOK(t, err)
			return report
		}
		if report := scan([]string{"Tree", "Tree/Child"}, nil); report.Tracks.Added != 1 {
			t.Fatalf("移入子树应入库一首：%+v", report)
		}
		id := presentTrackIDs(t, f, root.ID)["Tree/Child/01.flac"]
		moveFixture(t, root, "Tree", "Renamed")
		scan([]string{"Tree", "Renamed", "Renamed/Child"}, []string{"Tree"})
		requireTrackAt(t, presentTrackIDs(t, f, root.ID), "Renamed/Child/01.flac", id)
		directoryOK(t, os.Rename(filepath.Join(root.Path, "Renamed"), filepath.Join(staged.Path, "MovedOut")))
		if report := scan([]string{"Renamed"}, []string{"Renamed"}); report.Tracks.Missing != 1 {
			t.Fatalf("移出整个子树应报告一首缺失：%+v", report)
		}
		assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE id=$1 AND source_status='missing'`, 1, id)
	})

	for _, reason := range []string{"根失联", "读取失败", "取消"} {
		t.Run(reason, func(t *testing.T) {
			f := newLibraryFixture(t)
			s := scannerTestApplication(f)
			root := insertScannerRoot(t, f, "negative-scope")
			root.revision = 1
			const retained = "Album/retained.flac"
			writeDirectoryTrack(t, root, retained, "Album", 20)
			boundaryCompletedScan(t, s)
			trackID := presentTrackIDs(t, f, root.ID)[retained]
			if reason == "取消" {
				// 令局部扫描读完空目录后才暂停在终态事务，取消必须阻断 missing。
				directoryOK(t, os.Remove(filepath.Join(root.Path, retained)))
				gate := blockDirectoryProjection(t, f)
				id := startDirectoryScan(t, s, scopedDirectoryOptions(root, []string{"Album"}, nil))
				gate.wait(t)
				actor := uuid.New()
				directoryExec(t, f, `INSERT INTO users(id,username,password_hash,role) VALUES($1,'scan-test','synthetic','admin')`, actor)
				result, err := s.CancelScan(t.Context(), id, actor)
				directoryOK(t, err)
				if !result.Requested {
					t.Fatal("扫描尚未落终态，应接受取消请求")
				}
				gate.release(t)
				waitDirectoryScan(t, s, id, "canceled")
			} else {
				want := "succeeded"
				if reason == "根失联" {
					directoryOK(t, os.Rename(root.Path, root.Path+"-offline"))
					want = "incomplete"
				} else {
					writeFixtureFile(t, root, retained, []byte("fLaC"))
				}
				id := startDirectoryScan(t, s, scopedDirectoryOptions(root, []string{"Album"}, nil))
				waitDirectoryScan(t, s, id, want)
				report, err := s.GetScanReport(t.Context(), id, 1, 20)
				directoryOK(t, err)
				if report.Tracks.Missing != 0 || report.Issues.Recorded < 1 {
					t.Errorf("失败输入应留诊断且不报告缺失：%+v", report)
				}
			}
			requireTrackAt(t, presentTrackIDs(t, f, root.ID), retained, trackID)
		})
	}
}

type directoryProjectionGate struct {
	tx  *sql.Tx
	pid int
	db  *sql.DB
}

func blockDirectoryProjection(t *testing.T, f *libraryFixture) directoryProjectionGate {
	t.Helper()
	gate := beginDirectoryGate(t, f)
	directoryOK(t, ingest.LockProjectionWrites(t.Context(), gate.tx))
	return gate
}

func beginDirectoryGate(t *testing.T, f *libraryFixture) directoryProjectionGate {
	t.Helper()
	tx, err := f.database.BeginTx(t.Context(), nil)
	directoryOK(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	gate := directoryProjectionGate{tx: tx, db: f.database}
	directoryOK(t, tx.QueryRowContext(t.Context(), "SELECT pg_backend_pid()").Scan(&gate.pid))
	return gate
}

func (g directoryProjectionGate) wait(t *testing.T) {
	t.Helper()
	waitDirectoryCondition(t, "扫描等待投影写锁", func() bool {
		var blocked bool
		err := g.db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))`, g.pid).Scan(&blocked)
		directoryOK(t, err)
		return blocked
	})
}

func (g directoryProjectionGate) release(t *testing.T) {
	t.Helper()
	directoryOK(t, g.tx.Rollback())
}

// 实时时间只限制异步观察的等待上限，业务窗口由测试时钟或测试记录推进。
func waitDirectoryCondition(t *testing.T, description string, ready func() bool) {
	t.Helper()
	timeout := time.NewTimer(10 * time.Second)
	defer timeout.Stop()
	check := time.NewTicker(5 * time.Millisecond)
	defer check.Stop()
	for !ready() {
		select {
		case <-check.C:
		case <-timeout.C:
			t.Fatalf("未观察到%s", description)
		}
	}
}
