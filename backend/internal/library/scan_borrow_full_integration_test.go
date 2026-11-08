package library

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
)

func TestPostgreSQLBorrowFingerprintsAfterDirectoryRename(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "borrow-directory-rename")
	service := scannerTestApplication(fixture)
	var logs bytes.Buffer
	service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	var calls atomic.Int64
	service.fingerprinter = func(root registeredRoot) ingest.Fingerprinter {
		return recordingFingerprinter{inner: rootFingerprinter{root: root}, calls: &calls}
	}
	// 全零 MD5 强制首扫读取音频帧，后续零次调用才能证明没有整段重读。
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, zeroMD5: true, title: "One", album: "Album", track: 1})
	writeAudio(t, root, "Album/02.flac", audioFile{seed: 2, zeroMD5: true, title: "Two", album: "Album", track: 2})
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := presentTrackIDs(t, fixture, root.ID)
	if len(before) != 2 || calls.Load() != 2 {
		t.Fatalf("首扫曲目 = %v，指纹计算次数 = %d，期望两首曲目各计算一次", before, calls.Load())
	}
	releases := releaseIDsByTitle(t, fixture, root.ID)

	moveFixture(t, root, "Album", "Renamed")
	calls.Store(0)
	scan := runTestScan(t, fixture, service, root)
	requireScanStatus(t, scan, "succeeded")
	plan := borrowFullScanEvent(t, &logs, "library.scan.identity_resolved", scan.ID, root.ID)
	requireBorrowFullCount(t, plan, "fingerprints_borrowed", 2)
	requireBorrowFullCount(t, plan, "fingerprints_computed", 0)
	if got := calls.Load(); got != 0 {
		t.Fatalf("目录改名后整段读取了 %d 个文件，期望借用指纹且不再读取音频帧", got)
	}
	after := presentTrackIDs(t, fixture, root.ID)
	requireTrackAt(t, after, "Renamed/01.flac", before["Album/01.flac"])
	requireTrackAt(t, after, "Renamed/02.flac", before["Album/02.flac"])
	requireSameReleases(t, releases, releaseIDsByTitle(t, fixture, root.ID))
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 2, root.ID)
	requireNoMissingTracks(t, fixture, root.ID)
}

func TestPostgreSQLBorrowFingerprintsRejectsAmbiguousFileStats(t *testing.T) {
	for _, testCase := range []struct {
		name               string
		oldFiles, newFiles int
	}{
		{name: "旧侧不唯一", oldFiles: 2, newFiles: 1},
		{name: "新侧不唯一", oldFiles: 1, newFiles: 2},
		{name: "两侧都不唯一", oldFiles: 2, newFiles: 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			root := insertScannerRoot(t, fixture, "borrow-ambiguous")
			service := scannerTestApplication(fixture)
			var logs bytes.Buffer
			service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			var calls atomic.Int64
			service.fingerprinter = func(root registeredRoot) ingest.Fingerprinter {
				return recordingFingerprinter{inner: rootFingerprinter{root: root}, calls: &calls}
			}
			modifiedAt := time.Date(2026, time.January, 2, 3, 4, 5, 123456000, time.UTC)
			var size int64
			for index := 1; index <= testCase.oldFiles; index++ {
				relative := fmt.Sprintf("Old %d/01.flac", index)
				writeAudio(t, root, relative, audioFile{seed: byte(index), zeroMD5: true, title: "Track", album: "Album", track: 1})
				info := setBorrowFullFileTime(t, root, relative, modifiedAt)
				if index == 1 {
					size = info.Size()
				} else if info.Size() != size {
					t.Fatalf("歧义夹具大小不相等：%d 与 %d", info.Size(), size)
				}
			}
			requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
			before := presentTrackIDs(t, fixture, root.ID)
			if len(before) != testCase.oldFiles || calls.Load() != int64(testCase.oldFiles) {
				t.Fatalf("首扫曲目 = %v，指纹计算次数 = %d", before, calls.Load())
			}
			for index := 1; index <= testCase.oldFiles; index++ {
				oldPath := fmt.Sprintf("Old %d/01.flac", index)
				if index <= testCase.newFiles {
					moveFixture(t, root, oldPath, fmt.Sprintf("New %d/01.flac", index))
				} else if err := os.Remove(filepath.Join(root.Path, filepath.FromSlash(oldPath))); err != nil {
					t.Fatalf("删除本轮消失的合成文件：%v", err)
				}
			}
			for index := testCase.oldFiles + 1; index <= testCase.newFiles; index++ {
				relative := fmt.Sprintf("New %d/01.flac", index)
				writeAudio(t, root, relative, audioFile{seed: byte(index), zeroMD5: true, title: "Track", album: "Album", track: 1})
				if info := setBorrowFullFileTime(t, root, relative, modifiedAt); info.Size() != size {
					t.Fatalf("新侧歧义夹具大小 = %d，期望 %d", info.Size(), size)
				}
			}

			calls.Store(0)
			scan := runTestScan(t, fixture, service, root)
			requireScanStatus(t, scan, "succeeded")
			plan := borrowFullScanEvent(t, &logs, "library.scan.identity_resolved", scan.ID, root.ID)
			requireBorrowFullCount(t, plan, "fingerprints_borrowed", 0)
			requireBorrowFullCount(t, plan, "fingerprints_computed", int64(testCase.newFiles))
			if got := calls.Load(); got != int64(testCase.newFiles) {
				t.Fatalf("同名、同大小、同修改时间有歧义时计算了 %d 次指纹，期望 %d", got, testCase.newFiles)
			}
			after := presentTrackIDs(t, fixture, root.ID)
			if len(after) != testCase.newFiles {
				t.Fatalf("重扫后 present 曲目 = %v，期望 %d 首", after, testCase.newFiles)
			}
			// 不能借用不等于不能重绑定；实际帧指纹仍能唯一配对移动的原曲目。
			for index := 1; index <= min(testCase.oldFiles, testCase.newFiles); index++ {
				requireTrackAt(t, after, fmt.Sprintf("New %d/01.flac", index), before[fmt.Sprintf("Old %d/01.flac", index)])
			}
		})
	}
}

func TestPostgreSQLFullRescanParsesAllWithoutBorrowing(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "full-rescan")
	service := scannerTestApplication(fixture)
	var logs bytes.Buffer
	service.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	var calls atomic.Int64
	service.fingerprinter = func(root registeredRoot) ingest.Fingerprinter {
		return recordingFingerprinter{inner: rootFingerprinter{root: root}, calls: &calls}
	}
	modifiedAt := time.Date(2026, time.January, 2, 3, 4, 5, 123456000, time.UTC)
	for index, relative := range []string{"Stable/01.flac", "Stable/02.flac", "Moving/03.flac"} {
		writeAudio(t, root, relative, audioFile{seed: byte(index + 1), zeroMD5: true, title: "Old", album: filepath.Dir(relative), track: index + 1})
		setBorrowFullFileTime(t, root, relative, modifiedAt)
	}
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := presentTrackIDs(t, fixture, root.ID)
	if len(before) != 3 || calls.Load() != 3 {
		t.Fatalf("首扫曲目 = %v，指纹计算次数 = %d，期望三首曲目各计算一次", before, calls.Load())
	}
	releases := releaseIDsByTitle(t, fixture, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM library_parse_cache WHERE root_id=$1::uuid`, 3, root.ID)

	// 改标签但保留大小和修改时间：完整重扫必须越过已保存的旧标签观察。
	oldInfo, err := os.Stat(filepath.Join(root.Path, "Stable", "01.flac"))
	if err != nil {
		t.Fatal(err)
	}
	writeAudio(t, root, "Stable/01.flac", audioFile{seed: 1, zeroMD5: true, title: "New", album: "Stable", track: 1})
	if info := setBorrowFullFileTime(t, root, "Stable/01.flac", oldInfo.ModTime()); info.Size() != oldInfo.Size() {
		t.Fatalf("保留大小的标签修改改变了文件大小：%d → %d", oldInfo.Size(), info.Size())
	}
	// 同时移动一个文件，证明 full 不走增量扫描的唯一键借用捷径。
	moveFixture(t, root, "Moving", "Renamed")
	calls.Store(0)
	scan := runBorrowFullScan(t, fixture, service, root)
	requireScanStatus(t, scan, "succeeded")
	summary := borrowFullScanEvent(t, &logs, "library.scan.incremental_summary", scan.ID, root.ID)
	if summary["mode"] != ScanModeFull {
		t.Fatalf("完整重扫日志模式 = %v，期望 full", summary["mode"])
	}
	requireBorrowFullCount(t, summary, "files_parsed", 3)
	requireBorrowFullCount(t, summary, "parse_cache_hits", 0)
	requireBorrowFullCount(t, summary, "parse_cache_writes", 3)
	requireBorrowFullCount(t, summary, "candidates_written", 2)
	requireBorrowFullCount(t, summary, "candidates_skipped", 0)
	plan := borrowFullScanEvent(t, &logs, "library.scan.identity_resolved", scan.ID, root.ID)
	requireBorrowFullCount(t, plan, "fingerprints_borrowed", 0)
	requireBorrowFullCount(t, plan, "fingerprints_computed", 1)
	requireBorrowFullCount(t, plan, "fingerprints_reused", 2)
	if got := calls.Load(); got != 1 {
		t.Fatalf("完整重扫计算了 %d 次指纹，期望仅计算已移动文件，原路径文件仍复用指纹", got)
	}
	after := presentTrackIDs(t, fixture, root.ID)
	requireTrackAt(t, after, "Stable/01.flac", before["Stable/01.flac"])
	requireTrackAt(t, after, "Stable/02.flac", before["Stable/02.flac"])
	requireTrackAt(t, after, "Renamed/03.flac", before["Moving/03.flac"])
	var title string
	if err := fixture.database.QueryRow(`SELECT title FROM tracks WHERE id=$1::uuid`, before["Stable/01.flac"]).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "New" {
		t.Fatalf("完整重扫后的标签 = %q，期望 New，而不是缓存中的 Old", title)
	}
	requireSameReleases(t, releases, releaseIDsByTitle(t, fixture, root.ID))
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 3, root.ID)
	requireNoMissingTracks(t, fixture, root.ID)
}

func setBorrowFullFileTime(t *testing.T, root registeredRoot, relative string, modifiedAt time.Time) os.FileInfo {
	t.Helper()
	path := filepath.Join(root.Path, filepath.FromSlash(relative))
	if err := os.Chtimes(path, modifiedAt, modifiedAt); err != nil {
		t.Fatalf("设置合成文件修改时间：%v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(modifiedAt) {
		t.Fatalf("合成文件修改时间 = %s，期望 %s", info.ModTime(), modifiedAt)
	}
	return info
}

func runBorrowFullScan(t *testing.T, fixture *libraryFixture, service *Service, root registeredRoot) testScanResult {
	t.Helper()
	ctx := context.Background()
	scanID := insertRunningScan(t, fixture)
	if _, err := fixture.database.Exec(`UPDATE scan_runs SET scan_mode=$2 WHERE id=$1::uuid`, scanID, ScanModeFull); err != nil {
		t.Fatal(err)
	}
	connection, err := fixture.database.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err := service.scanRootWithOptions(ctx, connection, scanID, root, StartOptions{Mode: ScanModeFull}); err != nil {
		t.Fatalf("完整重扫失败：%v", err)
	}
	status, err := service.finalizeScan(ctx, connection, scanID, []string{root.ID}, scanOutcome{Complete: true})
	if err != nil {
		t.Fatalf("收口完整重扫：%v", err)
	}
	return testScanResult{ID: scanID, Status: status}
}

func borrowFullScanEvent(t *testing.T, logs *bytes.Buffer, event, scanID, rootID string) map[string]any {
	t.Helper()
	var found map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(logs.Bytes()), []byte{'\n'}) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("读取扫描日志：%v", err)
		}
		if record["event"] == event && record["scan_run_id"] == scanID && record["library_root_id"] == rootID {
			if found != nil {
				t.Fatalf("同一根重复记录扫描事件 %s", event)
			}
			found = record
		}
	}
	if found == nil {
		t.Fatalf("未找到扫描 %s 的根 %s 事件 %s", scanID, rootID, event)
	}
	return found
}

func requireBorrowFullCount(t *testing.T, record map[string]any, field string, want int64) {
	t.Helper()
	if got := record[field]; got != float64(want) {
		t.Fatalf("%s = %v，期望 %d", field, got, want)
	}
}
