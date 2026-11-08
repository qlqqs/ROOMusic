package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

type concurrentDiagnostic struct{ Path, Kind, Message string }
type concurrentStage struct {
	Path        string
	Observation map[string]any
}
type concurrentScanSnapshot struct {
	Status, Mode, ReportStatus string
	Skipped                    int64
	Progress                   ScanProgress
	Diagnostics                DiagnosticsSummary
	DiagnosticOrder            []concurrentDiagnostic
	Staged                     []concurrentStage
	Tracks                     []map[string]any
	Cache                      map[string]incrementalTestCacheEntry
	Summary                    incrementalTestSummary
	ReportFiles                ScanReportFiles
	ReportAlbums, ReportTracks ScanReportCounts
	ReportIssues               ScanReportIssues
	MissingReconciled          bool
}

func concurrentSnapshot(t *testing.T, fixture *libraryFixture, service *Service, logs *incrementalTestLogs, root registeredRoot) concurrentScanSnapshot {
	t.Helper()
	scan := runFilterLifecycleScan(t, service)
	if scan.Status != "succeeded" || scan.SkippedFiles == nil || scan.Progress == nil {
		t.Fatalf("扫描未成功：%+v", scan)
	}
	result := concurrentScanSnapshot{Status: scan.Status, Mode: scan.Mode, ReportStatus: scan.ReportStatus, Skipped: *scan.SkippedFiles, Progress: *scan.Progress, Diagnostics: scan.Diagnostics, Summary: logs.summary(t, scan.ID, root.ID)}
	result.Progress.UpdatedAt = time.Time{}
	result.Summary.ScanID = ""
	result.Summary.RootID = ""
	rows, err := fixture.database.Query(`SELECT relative_path,kind,message FROM scan_diagnostics WHERE scan_run_id=$1::uuid ORDER BY id`, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var d concurrentDiagnostic
		if err := rows.Scan(&d.Path, &d.Kind, &d.Message); err != nil {
			t.Fatal(err)
		}
		result.DiagnosticOrder = append(result.DiagnosticOrder, d)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	rows, err = fixture.database.Query(`SELECT relative_path,observation FROM concurrency_staged WHERE scan_run_id=$1::uuid ORDER BY sequence`, scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var stage concurrentStage
		var raw []byte
		if err := rows.Scan(&stage.Path, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &stage.Observation); err != nil {
			t.Fatal(err)
		}
		result.Staged = append(result.Staged, stage)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	rows, err = fixture.database.Query(`SELECT jsonb_build_object('path',t.relative_path,'title',t.title,'kind',t.source_kind,'fingerprint',t.content_fingerprint,'status',t.source_status,'album',r.title,'track',t.position,'disc',t.disc_number)
 FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id WHERE t.source_root_id=$1::uuid ORDER BY t.relative_path`, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var raw []byte
		var track map[string]any
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &track); err != nil {
			t.Fatal(err)
		}
		result.Tracks = append(result.Tracks, track)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	result.Cache = readIncrementalTestCache(t, fixture, root)
	for path, entry := range result.Cache {
		entry.SeenScanID = ""
		entry.UpdatedAt = time.Time{}
		result.Cache[path] = entry
	}
	report := incrementalTestReport(t, service, scan.ID)
	result.ReportFiles = report.Files
	result.ReportAlbums = report.Albums
	result.ReportTracks = report.Tracks
	result.ReportIssues = report.Issues
	result.MissingReconciled = report.MissingReconciled
	if len(result.DiagnosticOrder) != 100 || result.Diagnostics.Total != 100 || !report.Issues.MayBeTruncated {
		t.Fatalf("未覆盖诊断截断：%+v %+v", result.Diagnostics, report.Issues)
	}
	for index, d := range result.DiagnosticOrder {
		extension, kind := "flac", "parse_failure"
		if index%2 == 1 {
			extension, kind = "wma", "unsupported_format"
		}
		if d.Path != fmt.Sprintf("Errors/%03d.%s", index, extension) || d.Kind != kind {
			t.Fatalf("截断保留了错误的诊断，第 %d 条：%+v", index, d)
		}
	}
	if len(result.Staged) < 16 || len(result.Tracks) < 16 {
		t.Fatalf("未覆盖跨窗口暂存：staged=%d tracks=%d", len(result.Staged), len(result.Tracks))
	}
	return result
}

func TestPostgreSQLConcurrentScanMatchesSerialStagingDiagnosticsAndCache(t *testing.T) {
	// 两个隔离 schema 读取完全相同的文件及 mtime，避免把独立扫描生成的 UUID 当作业务差异。
	common := registeredRoot{Path: t.TempDir()}
	for index := 1; index <= 16; index++ {
		writeAudio(t, common, fmt.Sprintf("Album/%02d.flac", index), audioFile{seed: byte(index), title: fmt.Sprintf("Track %d", index), album: "Album", track: index, zeroMD5: index%2 == 0})
	}
	cue := func(reference string) []byte {
		return []byte(fmt.Sprintf("TITLE \"Album\"\nPERFORMER \"Artist\"\nFILE \"%s\" FLAC\n TRACK 01 AUDIO\n  TITLE \"First\"\n  INDEX 01 00:00:00\n TRACK 02 AUDIO\n  TITLE \"Second\"\n  INDEX 01 00:01:00\n", reference))
	}
	writeFixtureFile(t, common, "Album/00-before.cue", cue("01.flac"))
	writeFixtureFile(t, common, "Album/zz-after.cue", cue("02.flac"))
	writeFixtureFile(t, common, "Sheets/shared.cue", cue("../Album/01.flac"))
	for index := 0; index < 105; index++ {
		extension := "flac"
		if index%2 == 1 {
			extension = "wma"
		}
		writeFixtureFile(t, common, fmt.Sprintf("Errors/%03d.%s", index, extension), []byte("损坏音频"))
	}
	var serial [2]concurrentScanSnapshot
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("workers_%d", workers), func(t *testing.T) {
			fixture := newLibraryFixture(t)
			root := insertScannerRoot(t, fixture, "concurrent-shared")
			root.Path = common.Path
			if _, err := fixture.database.Exec(`UPDATE library_roots SET path=$1 WHERE id=$2::uuid`, root.Path, root.ID); err != nil {
				t.Fatal(err)
			}
			// 只在测试 schema 记录实际 INSERT 顺序；正式扫描仍正常清理暂存表。
			if _, err := fixture.database.Exec(`CREATE TABLE concurrency_staged(sequence BIGSERIAL,scan_run_id UUID,relative_path TEXT,observation JSONB);
 CREATE FUNCTION concurrency_capture_stage() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO concurrency_staged(scan_run_id,relative_path,observation) VALUES(NEW.scan_run_id,NEW.relative_path,NEW.observation); RETURN NEW; END $$;
 CREATE TRIGGER concurrency_capture_stage AFTER INSERT ON scan_observations FOR EACH ROW EXECUTE FUNCTION concurrency_capture_stage()`); err != nil {
				t.Fatal(err)
			}
			service, logs := newIncrementalTestService(t, fixture)
			service.WithScanConcurrency(workers)
			for pass := range 2 {
				snapshot := concurrentSnapshot(t, fixture, service, logs, root)
				if pass == 0 && snapshot.Summary.ParseCacheWrites != 16 {
					t.Fatalf("冷缓存没有恰好缓存 16 个成功文件：%+v", snapshot.Summary)
				}
				if pass == 1 && (snapshot.Summary.ParseCacheWrites != 0 || snapshot.Summary.FilesParsed != 53 || snapshot.Summary.ParseCacheHits <= 16) {
					t.Fatalf("热缓存仍解析成功音频或漏掉 CUE 命中：%+v", snapshot.Summary)
				}
				if workers == 1 {
					serial[pass] = snapshot
				} else {
					got, want := reflect.ValueOf(snapshot), reflect.ValueOf(serial[pass])
					for index := 0; index < got.NumField(); index++ {
						if !reflect.DeepEqual(got.Field(index).Interface(), want.Field(index).Interface()) {
							t.Errorf("第 %d 轮字段 %s 不一致：\n并发=%+v\n串行=%+v", pass, got.Type().Field(index).Name, got.Field(index).Interface(), want.Field(index).Interface())
						}
					}
				}
				t.Logf("workers=%d pass=%d staged=%d diagnostics=%d parsed=%d hits=%d cache_writes=%d", workers, pass, len(snapshot.Staged), len(snapshot.DiagnosticOrder), snapshot.Summary.FilesParsed, snapshot.Summary.ParseCacheHits, snapshot.Summary.ParseCacheWrites)
			}
		})
	}
}

func TestPostgreSQLScanPipelineCancelDiscardsInflightAndJoinsWorkers(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "pipeline-cancel")
	for index := 0; index < 4; index++ {
		writeAudio(t, root, fmt.Sprintf("%d.flac", index), audioFile{seed: byte(index), title: "取消测试", album: "Album", track: index + 1})
	}
	service := scannerTestApplication(fixture).WithScanConcurrency(4)
	scanID := insertRunningScan(t, fixture)
	connection, err := fixture.database.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stats := &incrementalStats{}
	cache := &parseCache{service: service, executor: connection, scanID: scanID, rootID: root.ID, mode: ScanModeIncremental, stats: stats}
	pipeline := newScanPipeline(ctx, service, connection, scanID, root, cache, nil, func(err error) { t.Errorf("暂存失败：%v", err) })
	defer pipeline.pool.Close()
	// 解析器不接受 context：用受控读取屏障表示正在读取的一个文件，不借助真实磁盘耗时猜测取消时机。
	reading := make(chan struct{}, 4)
	finishRead := make(chan struct{})
	defer func() {
		select {
		case <-finishRead:
		default:
			close(finishRead)
		}
	}()
	var active, started atomic.Int32
	for index := 0; index < 8; index++ {
		if err := pipeline.pool.Submit(func(context.Context) scanSlot {
			active.Add(1)
			started.Add(1)
			defer active.Add(-1)
			reading <- struct{}{}
			<-finishRead
			path := filepath.Join(root.Path, fmt.Sprintf("%d.flac", index%4))
			observation, parseErr := parser.ParseAudioFile(path)
			info, statErr := os.Stat(path)
			return scanSlot{relative: fmt.Sprintf("%d.flac", index%4), audio: []scanAudio{{path: path, relative: fmt.Sprintf("%d.flac", index%4), info: info, prepared: true, observation: observation, err: errors.Join(parseErr, statErr)}}}
		}); err != nil {
			t.Fatal(err)
		}
	}
	for range 4 {
		select {
		case <-reading:
		case <-time.After(5 * time.Second):
			t.Fatal("未启动四个读取任务")
		}
	}
	canceledAt := time.Now()
	cancel()
	// 所有当前读取在同一文件读取窗口内结束；其余四个排队任务不应启动。
	const singleReadWindow = 200 * time.Millisecond
	timer := time.AfterFunc(singleReadWindow, func() { close(finishRead) })
	defer timer.Stop()
	if err := pipeline.pool.Drain(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain=%v", err)
	}
	elapsed := time.Since(canceledAt)
	if active.Load() != 0 || started.Load() != 4 {
		t.Fatalf("取消后 worker 未归零或仍启动排队任务：active=%d started=%d", active.Load(), started.Load())
	}
	if elapsed > singleReadWindow+100*time.Millisecond {
		t.Fatalf("取消等待超过一个读取窗口：%v", elapsed)
	}
	if *stats != (incrementalStats{}) {
		t.Fatalf("取消后仍应用结果：%+v", stats)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_observations WHERE scan_run_id=$1::uuid`, 0, scanID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM library_parse_cache WHERE root_id=$1::uuid`, 0, root.ID)
	t.Logf("single_file_read_window=%v cancel_elapsed=%v active_workers=%d started=%d", singleReadWindow, elapsed, active.Load(), started.Load())
}
