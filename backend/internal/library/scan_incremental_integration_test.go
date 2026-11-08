package library

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"github.com/qlqq/roomusic/backend/internal/personal"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type incrementalTestSummary struct {
	Event                string `json:"event"`
	ScanID               string `json:"scan_run_id"`
	RootID               string `json:"library_root_id"`
	Mode                 string `json:"mode"`
	FilesParsed          int64  `json:"files_parsed"`
	ParseCacheHits       int64  `json:"parse_cache_hits"`
	ParseCacheWrites     int64  `json:"parse_cache_writes"`
	CandidatesWritten    int64  `json:"candidates_written"`
	CandidatesSkipped    int64  `json:"candidates_skipped"`
	ArtworkDeferredLoads int64  `json:"artwork_deferred_loads"`
}

// incrementalTestLogs 同时接收 worker 与取消轮询日志，读取时也持锁，避免测试收集器引入竞态。
type incrementalTestLogs struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (logs *incrementalTestLogs) Write(data []byte) (int, error) {
	logs.mu.Lock()
	defer logs.mu.Unlock()
	return logs.buffer.Write(data)
}

func (logs *incrementalTestLogs) summary(t *testing.T, scanID, rootID string) incrementalTestSummary {
	t.Helper()
	logs.mu.Lock()
	defer logs.mu.Unlock()
	var result incrementalTestSummary
	found := 0
	for _, line := range bytes.Split(logs.buffer.Bytes(), []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var entry incrementalTestSummary
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("读取扫描日志失败：%v", err)
		}
		if entry.Event == "library.scan.incremental_summary" && entry.ScanID == scanID && entry.RootID == rootID {
			result = entry
			found++
		}
	}
	if found != 1 {
		t.Fatalf("扫描 %s 的根 %s 有 %d 条增量摘要，期望恰好一条", scanID, rootID, found)
	}
	return result
}

func newIncrementalTestService(t *testing.T, fixture *libraryFixture) (*Service, *incrementalTestLogs) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	logs := &incrementalTestLogs{}
	service := NewService(ctx, fixture.database, filepath.Join(fixture.allowedRoot, "incremental-data"), slog.New(slog.NewJSONHandler(logs, nil)))
	t.Cleanup(func() {
		cancel()
		waitCtx, waitCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer waitCancel()
		if err := service.WaitForScans(waitCtx); err != nil {
			t.Errorf("等待扫描清理失败：%v", err)
		}
	})
	return service, logs
}

// runIncrementalTestScan 使用真实启动、worker 与终态路径，不能用只调 scanRoot 代替 missing 对账证据。
func runIncrementalTestScan(t *testing.T, service *Service, logs *incrementalTestLogs, root registeredRoot) (Scan, incrementalTestSummary) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started, err := service.StartScan(ctx)
	if err != nil || !started.Started {
		t.Fatalf("启动增量扫描失败：started=%v err=%v", started.Started, err)
	}
	if err := service.WaitForScans(ctx); err != nil {
		t.Fatalf("等待增量扫描失败：%v", err)
	}
	scan, err := service.GetScan(ctx, started.Scan.ID)
	if err != nil {
		t.Fatalf("读取增量扫描失败：%v", err)
	}
	if scan.Status != "succeeded" || scan.Mode != ScanModeIncremental || scan.Diagnostics.Total != 0 {
		t.Fatalf("扫描未完整成功或出现诊断：%+v", scan)
	}
	summary := logs.summary(t, scan.ID, root.ID)
	if summary.Mode != ScanModeIncremental {
		t.Fatalf("摘要模式为 %q，期望 incremental", summary.Mode)
	}
	return scan, summary
}

type incrementalTestTrack struct {
	ID, ReleaseID, SourceIdentity, Fingerprint string
	Size                                       int64
	ModifiedAt                                 time.Time
}

func readIncrementalTestTracks(t *testing.T, fixture *libraryFixture, root registeredRoot) map[string]incrementalTestTrack {
	t.Helper()
	rows, err := fixture.database.Query(`SELECT t.relative_path,t.id::text,m.release_id::text,t.source_identity,
		COALESCE(t.content_fingerprint,''),t.source_size,t.source_modified_at
		FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_root_id=$1::uuid AND t.source_status='present'`, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tracks := map[string]incrementalTestTrack{}
	for rows.Next() {
		var path string
		var track incrementalTestTrack
		if err := rows.Scan(&path, &track.ID, &track.ReleaseID, &track.SourceIdentity, &track.Fingerprint, &track.Size, &track.ModifiedAt); err != nil {
			t.Fatal(err)
		}
		tracks[path] = track
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tracks
}

type incrementalTestCacheEntry struct {
	SeenScanID, ParserVersion string
	Size                      int64
	ModifiedAt, UpdatedAt     time.Time
	Observation               parser.AudioObservation
}

func readIncrementalTestCache(t *testing.T, fixture *libraryFixture, root registeredRoot) map[string]incrementalTestCacheEntry {
	t.Helper()
	rows, err := fixture.database.Query(`SELECT relative_path,seen_scan_id::text,parser_version,source_size,
		source_modified_at,updated_at,observation FROM library_parse_cache WHERE root_id=$1::uuid`, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	entries := map[string]incrementalTestCacheEntry{}
	for rows.Next() {
		var path string
		var entry incrementalTestCacheEntry
		var raw []byte
		if err := rows.Scan(&path, &entry.SeenScanID, &entry.ParserVersion, &entry.Size, &entry.ModifiedAt, &entry.UpdatedAt, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &entry.Observation); err != nil {
			t.Fatal(err)
		}
		entries[path] = entry
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

func incrementalTestReport(t *testing.T, service *Service, scanID string) ScanReport {
	t.Helper()
	report, err := service.GetScanReport(context.Background(), scanID, 1, 20)
	if err != nil {
		t.Fatalf("读取扫描报告失败：%v", err)
	}
	if report.Status != "ready" || !report.MissingReconciled {
		t.Fatalf("扫描报告未完成完整 missing 对账：%+v", report)
	}
	return report
}

type incrementalTestCandidateState struct {
	ReleaseID, Signature string
	UpdatedAt            time.Time
}

func readIncrementalTestCandidateStates(t *testing.T, fixture *libraryFixture, root registeredRoot) map[string]incrementalTestCandidateState {
	t.Helper()
	rows, err := fixture.database.Query(`SELECT candidate_key,release_id::text,signature,updated_at
		FROM scan_candidate_states WHERE root_id=$1::uuid`, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	states := map[string]incrementalTestCandidateState{}
	for rows.Next() {
		var key string
		var state incrementalTestCandidateState
		if err := rows.Scan(&key, &state.ReleaseID, &state.Signature, &state.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		states[key] = state
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return states
}

// 结合摘要与逐候选状态，区分“确实跳过”与“重写但业务结果相同”。
func assertIncrementalTestCandidateRewrites(t *testing.T, before, after map[string]incrementalTestCandidateState, rewrittenRelease string) {
	t.Helper()
	if len(before) == 0 || len(after) != len(before) {
		t.Fatalf("候选状态数变化：前=%+v 后=%+v", before, after)
	}
	found := false
	for key, old := range before {
		current, ok := after[key]
		if !ok || current.ReleaseID != old.ReleaseID {
			t.Fatalf("候选身份变化：%s 前=%+v 后=%+v", key, old, current)
		}
		if old.ReleaseID == rewrittenRelease {
			found = true
			if current.Signature == old.Signature || !current.UpdatedAt.After(old.UpdatedAt) {
				t.Fatalf("变化候选未重写状态：%s 前=%+v 后=%+v", key, old, current)
			}
		} else if current != old {
			t.Fatalf("未变候选不应重写：%s 前=%+v 后=%+v", key, old, current)
		}
	}
	if !found {
		t.Fatalf("未找到应重写的发行：%s", rewrittenRelease)
	}
}

func TestPostgreSQLIncrementalParseCacheSecondScanPreservesIdentityAndReport(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "incremental-second-scan")
	service, logs := newIncrementalTestService(t, fixture)
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "First", album: "Album", track: 1})
	writeAudio(t, root, "Album/02.flac", audioFile{seed: 2, title: "Second", album: "Album", track: 2})
	writeAudio(t, root, "Other/01.flac", audioFile{seed: 3, title: "Other", album: "Other", track: 1})
	writeScanFixtureFile(t, root, "Album/cover.jpg", jpegArtworkOfWidth(3))
	_, first := runIncrementalTestScan(t, service, logs, root)
	if first.CandidatesWritten != 2 || first.CandidatesSkipped != 0 {
		t.Fatalf("首扫应写入两个候选：%+v", first)
	}
	if first.FilesParsed != 3 || first.ParseCacheWrites != 3 {
		t.Fatalf("首扫未解析并缓存所有音频：%+v", first)
	}
	before := readIncrementalTestTracks(t, fixture, root)
	if len(before) != 3 {
		t.Fatalf("首扫得到 %d 轨，期望 3", len(before))
	}
	cacheBefore := readIncrementalTestCache(t, fixture, root)
	statesBefore := readIncrementalTestCandidateStates(t, fixture, root)
	if len(statesBefore) != 2 {
		t.Fatalf("首扫候选状态不全：%+v", statesBefore)
	}

	scan, second := runIncrementalTestScan(t, service, logs, root)
	if second.FilesParsed != 0 || second.ParseCacheHits != 3 || second.ParseCacheWrites != 0 || second.CandidatesWritten != 0 || second.CandidatesSkipped != 2 {
		t.Fatalf("未变音频不应重新解析或覆盖缓存：%+v", second)
	}
	if after := readIncrementalTestTracks(t, fixture, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("重扫改变了曲目/Release 身份或音频来源事实：前=%+v 后=%+v", before, after)
	}
	if statesAfter := readIncrementalTestCandidateStates(t, fixture, root); !reflect.DeepEqual(statesBefore, statesAfter) {
		t.Fatalf("跳过候选不应改状态：前=%+v 后=%+v", statesBefore, statesAfter)
	}
	cacheAfter := readIncrementalTestCache(t, fixture, root)
	if len(cacheAfter) != 3 {
		t.Fatalf("第二扫丢失缓存：%+v", cacheAfter)
	}
	for path, entry := range cacheAfter {
		if entry.SeenScanID != scan.ID || !entry.UpdatedAt.Equal(cacheBefore[path].UpdatedAt) {
			t.Fatalf("缓存命中应只更新存活标记：%s %+v", path, entry)
		}
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_status='missing'`, 0, root.ID)
	report := incrementalTestReport(t, service, scan.ID)
	if report.Albums != (ScanReportCounts{}) || report.Tracks != (ScanReportCounts{}) || report.Files.Processed != 3 || report.Files.Counted != 3 {
		t.Fatalf("缓存命中应保持处理计数且报告零变化：%+v", report)
	}

	for _, mutation := range []struct {
		name   string
		apply  func()
		verify func()
	}{
		{"替换封面", func() {
			old, err := os.Stat(filepath.Join(root.Path, "Album/cover.jpg"))
			if err != nil {
				t.Fatal(err)
			}
			writeScanFixtureFile(t, root, "Album/cover.jpg", jpegArtworkOfWidth(7))
			setBorrowFullFileTime(t, root, "Album/cover.jpg", old.ModTime().Add(2*time.Second))
		}, func() { assertArtworks(t, fixture, root, "folder:image/jpeg:7") }},
		{"新增EAC日志", func() {
			writeScanFixtureFile(t, root, "Album/rip.log", []byte("Exact Audio Copy V1.6\nEAC extraction logfile"))
		}, func() {
			assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE id=$1::uuid AND source_type='CD' AND media_type='CD'`, 1, before["Album/01.flac"].ReleaseID)
			assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_grouping_evidence WHERE release_id=$1::uuid AND source_refs::text LIKE '%Album/rip.log%'`, 1, before["Album/01.flac"].ReleaseID)
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			statesBefore := readIncrementalTestCandidateStates(t, fixture, root)
			mutation.apply()
			_, summary := runIncrementalTestScan(t, service, logs, root)
			if summary.FilesParsed != 0 || summary.ParseCacheHits != 3 || summary.CandidatesWritten != 1 || summary.CandidatesSkipped != 1 {
				t.Fatalf("附属文件变化应只重写所在候选，不重新解析：%+v", summary)
			}
			assertIncrementalTestCandidateRewrites(t, statesBefore, readIncrementalTestCandidateStates(t, fixture, root), before["Album/01.flac"].ReleaseID)
			if after := readIncrementalTestTracks(t, fixture, root); !reflect.DeepEqual(before, after) {
				t.Fatalf("附属文件变化改变曲目身份或来源事实：前=%+v 后=%+v", before, after)
			}
			mutation.verify()
			requireNoMissingTracks(t, fixture, root.ID)
		})
	}
}

func TestPostgreSQLIncrementalParseCacheTagMtimeChangeOnlyReparsesChangedFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 可绕过文件权限")
	}
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "incremental-tag-change")
	service, logs := newIncrementalTestService(t, fixture)
	for track := 1; track <= 2; track++ {
		writeAudio(t, root, fmt.Sprintf("Album/%02d.flac", track), audioFile{seed: byte(track), title: fmt.Sprintf("Track %d", track), album: "Album", track: track})
	}
	writeAudio(t, root, "Other/01.flac", audioFile{seed: 3, title: "Track 3", album: "Other", track: 1})
	runIncrementalTestScan(t, service, logs, root)
	before := readIncrementalTestTracks(t, fixture, root)
	cacheBefore := readIncrementalTestCache(t, fixture, root)
	statesBefore := readIncrementalTestCandidateStates(t, fixture, root)
	const changed = "Album/02.flac"
	// 等长标签保持大小和码率不变，显式拉开 mtime，不依赖宿主文件系统的写入时间粒度。
	writeAudio(t, root, changed, audioFile{seed: 2, title: "Fresh 2", album: "Album", track: 2})
	modifiedAt := cacheBefore[changed].ModifiedAt.Add(2 * time.Second)
	if err := os.Chtimes(filepath.Join(root.Path, filepath.FromSlash(changed)), modifiedAt, modifiedAt); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(root.Path, "Album/01.flac"), filepath.Join(root.Path, "Other/01.flac")} {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatalf("禁止音频读取失败：%v", err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(path, 0o644); err != nil {
				t.Errorf("恢复音频读取权限失败：%v", err)
			}
		})
	}
	scan, summary := runIncrementalTestScan(t, service, logs, root)
	if scan.SkippedFiles == nil || *scan.SkippedFiles != 0 {
		t.Fatalf("缓存命中不应跳过不可读的音频：%+v", scan)
	}
	if summary.FilesParsed != 1 || summary.ParseCacheHits != 2 || summary.ParseCacheWrites != 1 || summary.CandidatesWritten != 1 || summary.CandidatesSkipped != 1 {
		t.Fatalf("只应重解析改标签的音频：%+v", summary)
	}
	assertIncrementalTestCandidateRewrites(t, statesBefore, readIncrementalTestCandidateStates(t, fixture, root), before[changed].ReleaseID)
	after := readIncrementalTestTracks(t, fixture, root)
	if len(after) != 3 {
		t.Fatalf("改标签改变曲目数：%+v", after)
	}
	for path, old := range before {
		current := after[path]
		if path == changed {
			if current.ID != old.ID || current.ReleaseID != old.ReleaseID || current.Fingerprint != old.Fingerprint || current.Size != old.Size || !current.ModifiedAt.Equal(modifiedAt) {
				t.Fatalf("标签修改不应替换内容身份：前=%+v 后=%+v", old, current)
			}
		} else if current != old {
			t.Fatalf("未变音频的来源事实变化：%s 前=%+v 后=%+v", path, old, current)
		}
	}
	cacheAfter := readIncrementalTestCache(t, fixture, root)
	for path, old := range cacheBefore {
		current := cacheAfter[path]
		if current.SeenScanID != scan.ID {
			t.Fatalf("缓存未记本轮存活：%s %+v", path, current)
		}
		if path == changed {
			if current.Observation.Title != "Fresh 2" || current.Size != old.Size || !current.ModifiedAt.Equal(modifiedAt) {
				t.Fatalf("变化音频的缓存未更新：%+v", current)
			}
		} else if !current.UpdatedAt.Equal(old.UpdatedAt) || !reflect.DeepEqual(current.Observation, old.Observation) {
			t.Fatalf("未变音频被重新写缓存：%s %+v", path, current)
		}
	}
	var title string
	if err := fixture.database.QueryRow(`SELECT title FROM tracks WHERE source_root_id=$1::uuid AND relative_path=$2 AND source_status='present'`, root.ID, changed).Scan(&title); err != nil || title != "Fresh 2" {
		t.Fatalf("新标签未入库：title=%q err=%v", title, err)
	}
	report := incrementalTestReport(t, service, scan.ID)
	if report.Tracks != (ScanReportCounts{Updated: 1}) || report.Files.Processed != 3 {
		t.Fatalf("改标签应只报告一轨更新：%+v", report)
	}
}

// writeIncrementalTestMP3 生成带可选 APIC 的 ID3v2.3 文件；seed 只改变音频帧，标签不影响内容身份。
func writeIncrementalTestMP3(t *testing.T, root registeredRoot, relative string, seed byte, track int, artwork []byte) {
	t.Helper()
	var tags []byte
	appendFrame := func(id string, payload []byte) {
		header := make([]byte, 10)
		copy(header, id)
		binary.BigEndian.PutUint32(header[4:8], uint32(len(payload)))
		tags = append(tags, header...)
		tags = append(tags, payload...)
	}
	for _, tag := range []struct{ id, value string }{
		{"TIT2", fmt.Sprintf("Track %d", track)}, {"TPE1", "Artist"}, {"TALB", "Album"}, {"TRCK", fmt.Sprint(track)},
	} {
		appendFrame(tag.id, append([]byte{3}, tag.value...))
	}
	if len(artwork) > 0 {
		picture := append([]byte{0}, []byte("image/png")...)
		picture = append(picture, 0, 3, 0)
		appendFrame("APIC", append(picture, artwork...))
	}
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(len(tags)>>21) & 0x7f, byte(len(tags)>>14) & 0x7f, byte(len(tags)>>7) & 0x7f, byte(len(tags)) & 0x7f}
	frame := bytes.Repeat([]byte{seed}, 417)
	copy(frame, []byte{0xff, 0xfb, 0x90, 0x00})
	content := append(header, tags...)
	content = append(content, bytes.Repeat(frame, 8)...)
	writeScanFixtureFile(t, root, relative, content)
}

func assertIncrementalTestManagedArtwork(t *testing.T, fixture *libraryFixture, service *Service, root registeredRoot, want []byte) string {
	t.Helper()
	var releaseID, sourceType, mimeType, key string
	if err := fixture.database.QueryRow(`SELECT r.id::text,a.source_type,a.mime_type,a.storage_key FROM releases r
		JOIN release_artworks a ON a.release_id=r.id WHERE r.source_root_id=$1::uuid`, root.ID).Scan(&releaseID, &sourceType, &mimeType, &key); err != nil {
		t.Fatalf("读取封面关系失败：%v", err)
	}
	if sourceType != "embedded" || mimeType != "image/png" {
		t.Fatalf("应绑定内嵌封面，实际为 %s/%s", sourceType, mimeType)
	}
	data, err := os.ReadFile(filepath.Join(service.dataDirectory, "artwork", key))
	if err != nil || !bytes.Equal(data, want) {
		t.Fatalf("受管封面字节不符：err=%v bytes=%x", err, data)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_artworks a JOIN releases r ON r.id=a.release_id WHERE r.source_root_id=$1::uuid`, 1, root.ID)
	return releaseID
}

func TestPostgreSQLIncrementalParseCacheDeferredArtworkSurvivesAddedTrack(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "incremental-deferred-artwork")
	service, logs := newIncrementalTestService(t, fixture)
	artwork := pngArtworkOfWidth(9)
	writeIncrementalTestMP3(t, root, "Album/01.mp3", 1, 1, artwork)
	writeIncrementalTestMP3(t, root, "Album/02.mp3", 2, 2, nil)
	writeScanFixtureFile(t, root, "Album/cover.png", pngArtworkOfWidth(3))
	runIncrementalTestScan(t, service, logs, root)
	releaseID := assertIncrementalTestManagedArtwork(t, fixture, service, root, artwork)
	before := readIncrementalTestTracks(t, fixture, root)
	cache := readIncrementalTestCache(t, fixture, root)
	if entry := cache["Album/01.mp3"]; len(entry.Observation.Artwork) != 0 || entry.Observation.ArtworkMIME != "image/png" {
		t.Fatalf("缓存必须去掉封面字节但保留 MIME：%+v", entry.Observation)
	}
	writeIncrementalTestMP3(t, root, "Album/03.mp3", 3, 3, nil)
	scan, summary := runIncrementalTestScan(t, service, logs, root)
	if summary.FilesParsed != 1 || summary.ParseCacheHits != 2 || summary.ParseCacheWrites != 1 || summary.ArtworkDeferredLoads != 1 {
		t.Fatalf("旧轨应命中缓存，封面在保存时延迟读取：%+v", summary)
	}
	if current := assertIncrementalTestManagedArtwork(t, fixture, service, root, artwork); current != releaseID {
		t.Fatalf("新增一轨不应替换 Release：前=%s 后=%s", releaseID, current)
	}
	after := readIncrementalTestTracks(t, fixture, root)
	if len(after) != 3 {
		t.Fatalf("新增轨未保存：%+v", after)
	}
	for path, old := range before {
		if current := after[path]; current != old {
			t.Fatalf("新增轨改变旧轨身份或来源事实：%s 前=%+v 后=%+v", path, old, current)
		}
	}
	report := incrementalTestReport(t, service, scan.ID)
	if report.Tracks != (ScanReportCounts{Added: 1}) {
		t.Fatalf("新增轨报告不符：%+v", report)
	}
}

func TestPostgreSQLIncrementalParseCacheCueSecondParentKeepsEmbeddedArtwork(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 可绕过文件权限")
	}
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "incremental-cue-second-artwork")
	service, logs := newIncrementalTestService(t, fixture)
	artwork := pngArtworkOfWidth(7)
	writeIncrementalTestMP3(t, root, "Album/a-first.mp3", 1, 1, nil)
	writeIncrementalTestMP3(t, root, "Album/b-second.mp3", 2, 2, artwork)
	// sheet 排在父文件之前，既覆盖父音频缓存复用，也避免只靠物理轨的封面兜底通过。
	// 每父至少两轨，避免命中现有的单轨 CUE 与物理音频合并规则；MP3 时长约 15 帧。
	writeScanFixtureFile(t, root, "Album/00-album.cue", []byte("TITLE \"Album\"\nPERFORMER \"Artist\"\nFILE \"a-first.mp3\" MP3\n TRACK 01 AUDIO\n  TITLE \"First\"\n  INDEX 01 00:00:00\n TRACK 02 AUDIO\n  TITLE \"First Second\"\n  INDEX 01 00:00:05\nFILE \"b-second.mp3\" MP3\n TRACK 03 AUDIO\n  TITLE \"Second\"\n  INDEX 01 00:00:00\n TRACK 04 AUDIO\n  TITLE \"Second Second\"\n  INDEX 01 00:00:05\n"))
	runIncrementalTestScan(t, service, logs, root)
	releaseID := assertIncrementalTestManagedArtwork(t, fixture, service, root, artwork)
	before := readIncrementalTestTracks(t, fixture, root)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_kind='cue_virtual' AND source_status='present'`, 4, root.ID)
	cache := readIncrementalTestCache(t, fixture, root)
	if len(cache) != 2 || cache["Album/a-first.mp3"].Observation.ArtworkMIME != "" || cache["Album/b-second.mp3"].Observation.ArtworkMIME != "image/png" || len(cache["Album/b-second.mp3"].Observation.Artwork) != 0 {
		t.Fatalf("应只有第二父音频的无字节封面标记：%+v", cache)
	}
	statesBefore := readIncrementalTestCandidateStates(t, fixture, root)
	sheetPath := filepath.Join(root.Path, "Album/00-album.cue")
	sheet, err := os.ReadFile(sheetPath)
	if err != nil {
		t.Fatal(err)
	}
	// 父音频未变，仅给 sheet 增加 REM：签名必须失效，保留原 deferred 封面断言。
	writeScanFixtureFile(t, root, "Album/00-album.cue", append(sheet, []byte("REM COMMENT \"signature changed\"\n")...))
	for _, path := range []string{filepath.Join(root.Path, "Album/a-first.mp3")} {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatalf("禁止音频读取失败：%v", err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(path, 0o644); err != nil {
				t.Errorf("恢复音频读取权限失败：%v", err)
			}
		})
	}
	scan, summary := runIncrementalTestScan(t, service, logs, root)
	if scan.SkippedFiles == nil || *scan.SkippedFiles != 0 {
		t.Fatalf("缓存命中不应跳过不可读的音频：%+v", scan)
	}
	if summary.FilesParsed != 0 || summary.ParseCacheWrites != 0 || summary.ArtworkDeferredLoads != 1 || summary.CandidatesWritten != 1 || summary.CandidatesSkipped != 0 {
		t.Fatalf("多父 CUE 命中缓存后只应延迟读取第二父封面：%+v", summary)
	}
	assertIncrementalTestCandidateRewrites(t, statesBefore, readIncrementalTestCandidateStates(t, fixture, root), releaseID)
	if current := assertIncrementalTestManagedArtwork(t, fixture, service, root, artwork); current != releaseID {
		t.Fatalf("缓存重扫替换了 CUE Release：前=%s 后=%s", releaseID, current)
	}
	if after := readIncrementalTestTracks(t, fixture, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("CUE 缓存重扫改变了身份或来源事实：前=%+v 后=%+v", before, after)
	}
}

func TestPostgreSQLIncrementalParseCacheCueWithoutArtworkDoesNotOpenParents(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 可绕过文件权限")
	}
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "incremental-cue-no-artwork")
	service, logs := newIncrementalTestService(t, fixture)
	writeAudio(t, root, "Parents/a.flac", audioFile{seed: 1, title: "First", album: "Album", track: 1})
	writeAudio(t, root, "Parents/b.flac", audioFile{seed: 2, title: "Second", album: "Album", track: 2})
	// 合成 FLAC 长 240 秒，每父的两个索引都在有效区间内。
	writeScanFixtureFile(t, root, "Sheets/album.cue", []byte("TITLE \"Album\"\nPERFORMER \"Artist\"\nFILE \"../Parents/a.flac\" FLAC\n TRACK 01 AUDIO\n  INDEX 01 00:00:00\n TRACK 02 AUDIO\n  INDEX 01 00:01:00\nFILE \"../Parents/b.flac\" FLAC\n TRACK 03 AUDIO\n  INDEX 01 00:00:00\n TRACK 04 AUDIO\n  INDEX 01 00:01:00\n"))
	_, first := runIncrementalTestScan(t, service, logs, root)
	if first.FilesParsed != 2 {
		t.Fatalf("首扫跨目录父音频只应解析一次：%+v", first)
	}
	before := readIncrementalTestTracks(t, fixture, root)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_kind='cue_virtual' AND source_status='present'`, 4, root.ID)
	for _, path := range []string{filepath.Join(root.Path, "Parents/a.flac"), filepath.Join(root.Path, "Parents/b.flac")} {
		if err := os.Chmod(path, 0); err != nil {
			t.Fatalf("禁止音频读取失败：%v", err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(path, 0o644); err != nil {
				t.Errorf("恢复音频读取权限失败：%v", err)
			}
		})
	}
	scan, second := runIncrementalTestScan(t, service, logs, root)
	if scan.SkippedFiles == nil || *scan.SkippedFiles != 0 {
		t.Fatalf("缓存命中不应跳过不可读的音频：%+v", scan)
	}
	if second.FilesParsed != 0 || second.ParseCacheWrites != 0 || second.ParseCacheHits < 2 || second.ArtworkDeferredLoads != 0 {
		t.Fatalf("无封面 CUE 不应重新解析父音频：%+v", second)
	}
	if after := readIncrementalTestTracks(t, fixture, root); !reflect.DeepEqual(before, after) {
		t.Fatalf("CUE 缓存重扫改变了虚拟轨身份或来源事实：前=%+v 后=%+v", before, after)
	}
	if cache := readIncrementalTestCache(t, fixture, root); len(cache) != 2 {
		t.Fatalf("CUE 只应缓存两个物理父文件：%+v", cache)
	}
	report := incrementalTestReport(t, service, scan.ID)
	if report.Tracks != (ScanReportCounts{}) || report.Albums != (ScanReportCounts{}) || report.Files.Processed != 3 {
		t.Fatalf("CUE 缓存重扫应报告零变化且处理 sheet 和父音频：%+v", report)
	}
	assertArtworks(t, fixture, root)
}

func TestPostgreSQLIncrementalParseCacheCompleteTraversalCleansDeletedFile(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "incremental-cache-cleanup")
	service, logs := newIncrementalTestService(t, fixture)
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "Keep", album: "Album", track: 1})
	writeAudio(t, root, "Other/02.flac", audioFile{seed: 2, title: "Delete", album: "Other", track: 2})
	runIncrementalTestScan(t, service, logs, root)
	before := readIncrementalTestTracks(t, fixture, root)
	if cache := readIncrementalTestCache(t, fixture, root); len(cache) != 2 {
		t.Fatalf("首扫缓存未写入：%+v", cache)
	}
	statesBefore := readIncrementalTestCandidateStates(t, fixture, root)
	if len(statesBefore) != 2 {
		t.Fatalf("首扫候选状态不全：%+v", statesBefore)
	}
	// 保留一轨，删除路径不会触发空根保护，必须实际走完整遍历和 missing 对账。
	if err := os.Remove(filepath.Join(root.Path, "Other/02.flac")); err != nil {
		t.Fatal(err)
	}
	scan, summary := runIncrementalTestScan(t, service, logs, root)
	if summary.FilesParsed != 0 || summary.ParseCacheHits != 1 || summary.ParseCacheWrites != 0 {
		t.Fatalf("删除来源后只应命中幸存文件：%+v", summary)
	}
	statesAfter := readIncrementalTestCandidateStates(t, fixture, root)
	if len(statesAfter) != 1 {
		t.Fatalf("消失候选状态未清理：%+v", statesAfter)
	}
	for key, state := range statesAfter {
		if state.ReleaseID != before["Album/01.flac"].ReleaseID || state != statesBefore[key] {
			t.Fatalf("幸存候选状态变化：%+v", state)
		}
	}
	cache := readIncrementalTestCache(t, fixture, root)
	if len(cache) != 1 || cache["Album/01.flac"].SeenScanID != scan.ID {
		t.Fatalf("完整遍历后未删除消失缓存或丢失幸存缓存：%+v", cache)
	}
	after := readIncrementalTestTracks(t, fixture, root)
	if len(after) != 1 || after["Album/01.flac"] != before["Album/01.flac"] {
		t.Fatalf("删除其它轨改变幸存轨身份或来源事实：%+v", after)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE id=$1::uuid AND source_status='missing'`, 1, before["Other/02.flac"].ID)
	report := incrementalTestReport(t, service, scan.ID)
	if report.Tracks != (ScanReportCounts{Missing: 1}) || report.Files.Processed != 1 {
		t.Fatalf("删除文件的报告不符：%+v", report)
	}
}

func TestPostgreSQLIncrementalCandidateStateFormatSplitSkipKeepsVersionGroupAndFavorites(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "incremental-format-split")
	service, logs := newIncrementalTestService(t, fixture)
	paths := map[string]int{}
	for position := 1; position <= 2; position++ {
		flacPath, mp3Path := fmt.Sprintf("Album/%02d.flac", position), fmt.Sprintf("Album/%02d.mp3", position)
		writeAudio(t, root, flacPath, audioFile{seed: byte(position), title: fmt.Sprintf("Track %d", position), album: "Album", track: position})
		writeScanFixtureFile(t, root, mp3Path, formatSplitMP3(position))
		paths[flacPath], paths[mp3Path] = position, position
	}
	_, first := runIncrementalTestScan(t, service, logs, root)
	if first.CandidatesWritten != 2 || first.CandidatesSkipped != 0 {
		t.Fatalf("首扫应写入两个格式候选：%+v", first)
	}
	before := readFormatSplitPlacements(t, fixture, root.ID, paths)
	flac, mp3 := before["Album/01.flac"], before["Album/01.mp3"]
	if flac.ReleaseID == mp3.ReleaseID || flac.GroupID != mp3.GroupID {
		t.Fatalf("夹具未得到同组两个格式版本：%+v", before)
	}
	statesBefore := readIncrementalTestCandidateStates(t, fixture, root)
	userID := uuid.New()
	if _, err := fixture.database.Exec(`INSERT INTO users(id,username,password_hash,role) VALUES($1::uuid,'incremental-favorite','test-only','user')`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Exec(`INSERT INTO personal_favorite_state(user_id,revision) VALUES($1::uuid,10)`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Exec(`INSERT INTO favorite_albums(user_id,group_id,saved_title,saved_artist,created_at)
		VALUES($1::uuid,$2::uuid,'保存专辑','保存艺人','2026-01-01T00:00:00Z')`, userID, flac.GroupID); err != nil {
		t.Fatal(err)
	}
	favoriteMoves := 0
	mover := personal.NewService(fixture.database)
	service.SetAlbumFavoriteMover(func(ctx context.Context, tx *sql.Tx, moves map[string]string) (int, error) {
		favoriteMoves++
		return mover.MoveAlbumFavorites(ctx, tx, moves)
	})

	// flac 的候选键先于 mp3：先让它跳过，再给后写的 mp3 版本补上一轨。
	writeScanFixtureFile(t, root, "Album/03.mp3", formatSplitMP3(3))
	scan, summary := runIncrementalTestScan(t, service, logs, root)
	if summary.FilesParsed != 1 || summary.ParseCacheHits != 4 || summary.CandidatesWritten != 1 || summary.CandidatesSkipped != 1 {
		t.Fatalf("应跳过 flac，只重写新增文件所在的 mp3 候选：%+v", summary)
	}
	assertIncrementalTestCandidateRewrites(t, statesBefore, readIncrementalTestCandidateStates(t, fixture, root), mp3.ReleaseID)
	after := readIncrementalTestTracks(t, fixture, root)
	for path, placement := range before {
		current := after[path]
		if current.ID != placement.TrackID || current.ReleaseID != placement.ReleaseID {
			t.Fatalf("格式候选混合跳过/重写改变旧身份：%s 前=%+v 后=%+v", path, placement, current)
		}
	}
	added := after["Album/03.mp3"]
	if len(after) != 5 || added.ReleaseID != mp3.ReleaseID || added.ID == "" {
		t.Fatalf("新曲目未加入原 mp3 版本：%+v", after)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE id IN ($1::uuid,$2::uuid) AND group_id=$3::uuid`, 2, flac.ReleaseID, mp3.ReleaseID, flac.GroupID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE id=$1::uuid AND position=3 AND codec='mp3'`, 1, added.ID)
	if favoriteMoves != 0 {
		t.Fatalf("同组格式版本补轨不应调用收藏转移：%d", favoriteMoves)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_groups`, 1)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM releases WHERE source_root_id=$1::uuid`, 2, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM favorite_albums WHERE user_id=$1::uuid`, 1, userID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM favorite_albums WHERE user_id=$1::uuid AND group_id=$2::uuid
		AND saved_title='保存专辑' AND saved_artist='保存艺人' AND created_at='2026-01-01T00:00:00Z'`, 1, userID, flac.GroupID)
	assertDatabaseCount(t, fixture, `SELECT revision FROM personal_favorite_state WHERE user_id=$1::uuid`, 10, userID)
	requireNoMissingTracks(t, fixture, root.ID)
	report := incrementalTestReport(t, service, scan.ID)
	if report.Tracks != (ScanReportCounts{Added: 1}) || report.Albums != (ScanReportCounts{Updated: 1}) {
		t.Fatalf("格式版本补轨不应新建组或报告旧轨变化：%+v", report)
	}
}
