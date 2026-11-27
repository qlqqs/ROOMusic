package library

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog/ingest"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"golang.org/x/sys/unix"
)

// 记录打开与关闭，避免 inotify 合并连续的同类打开事件。
func scanReadOpenCounter(t *testing.T, files ...string) func() map[string]int {
	t.Helper()
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(fd) })
	names := make(map[int32]string)
	for _, path := range files {
		wd, err := unix.InotifyAddWatch(fd, path, unix.IN_OPEN|unix.IN_CLOSE_NOWRITE|unix.IN_CLOSE_WRITE)
		if err != nil {
			t.Fatal(err)
		}
		names[int32(wd)] = path
	}
	return func() map[string]int {
		t.Helper()
		var buf [4096]byte
		result := make(map[string]int)
		for {
			n, err := unix.Read(fd, buf[:])
			if errors.Is(err, unix.EAGAIN) {
				return result
			}
			if err != nil {
				t.Fatal(err)
			}
			for offset := 0; offset < n; {
				wd := int32(binary.NativeEndian.Uint32(buf[offset:]))
				mask := binary.NativeEndian.Uint32(buf[offset+4:])
				if mask&unix.IN_Q_OVERFLOW != 0 {
					t.Fatal("打开事件队列超限")
				}
				if mask&unix.IN_OPEN != 0 {
					result[names[wd]]++
				}
				offset += unix.SizeofInotifyEvent + int(binary.NativeEndian.Uint32(buf[offset+12:]))
			}
		}
	}
}

func TestPostgreSQLScanReadFormatCache(t *testing.T) {
	t.Run("candidate_formats_cue_and_artwork", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		root := insertScannerRoot(t, fixture, "read-formats")
		service, logs := newIncrementalTestService(t, fixture)
		const wrong, extensionless, sheet = "Flac/01.mp3", "M4A/image", "M4A/00.cue"
		cover := pngArtworkOfWidth(9)
		writeAudio(t, root, wrong, audioFile{seed: 12, title: "FLAC title", album: "Wrong suffix", track: 1})
		writeFixtureFile(t, root, extensionless, artworkPriorityM4A(cover))
		writeFixtureFile(t, root, sheet, filterCueDocument("image"))
		writeFixtureFile(t, root, "Flac/photo.png", pngArtworkOfWidth(5))
		writeFixtureFile(t, root, "Flac/notes.txt", []byte("text is ignored"))
		writeAudio(t, root, "Other/valid.wma", audioFile{seed: 13, title: "Audio in WMA path", album: "Old diagnostic suffix", track: 1})
		cuePath := filepath.Join(root.Path, sheet)
		imagePath := filepath.Join(root.Path, "Flac/photo.png")
		textPath := filepath.Join(root.Path, "Flac/notes.txt")
		opens := scanReadOpenCounter(t, cuePath, imagePath, textPath)
		first, summary := runIncrementalTestScan(t, service, logs, root)
		if progress := first.Progress; progress == nil || progress.FilesCounted != 4 || progress.FilesProcessed != 4 {
			t.Errorf("预计数与已处理数应均为 4：%+v", progress)
		}
		if n := opens(); n[cuePath] != 1 || n[imagePath] != 1 || n[textPath] != 0 {
			t.Errorf("CUE/图片/文本打开次数 = %d/%d/%d，期望 1/1/0（图片附件读取尺寸）", n[cuePath], n[imagePath], n[textPath])
		}
		if summary.FilesParsed != 3 {
			t.Errorf("判型读取次数 = %d，期望 3", summary.FilesParsed)
		}
		cache := readIncrementalTestCache(t, fixture, root)
		for path, format := range map[string]string{wrong: ".flac", extensionless: ".m4a", "Other/valid.wma": ".flac"} {
			if e := cache[path]; e.Observation.Format != format || e.ParserVersion != parser.CacheVersion(format) || e.SeenScanID != first.ID {
				t.Errorf("%s 真实格式缓存 = %+v，期望 %q/%q", path, e, format, parser.CacheVersion(format))
			}
		}
		assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1 AND source_kind='cue_virtual' AND cue_parent_relative_path=$2 AND source_status='present'`, 2, root.ID, extensionless)
		original := readIncrementalTestTracks(t, fixture, root)
		requireScanReadArtwork(t, fixture, service, root, extensionless, cover)
		_, summary = runIncrementalTestScan(t, service, logs, root)
		if summary.ParseCacheHits != 4 || summary.FilesParsed != 0 {
			t.Errorf("第二次扫描应按真实格式缓存，含 CUE 父文件复用：%+v", summary)
		}
		_ = opens()
		// 只改 sheet，使候选重写，从缓存的父文件事实重新读取内嵌封面。
		writeFixtureFile(t, root, sheet, append(filterCueDocument("image"), []byte("\nREM COMMENT \"changed sheet\"\n")...))
		moveFixture(t, root, wrong, "Flac/01-renamed")
		rewritten, summary := runIncrementalTestScan(t, service, logs, root)
		if summary.ArtworkDeferredLoads != 1 || summary.FilesParsed != 1 {
			t.Errorf("应只重新解析改名文件，并延迟读取 CUE 父封面：%+v", summary)
		}
		cached := readIncrementalTestCache(t, fixture, root)
		renamed := readIncrementalTestTracks(t, fixture, root)["Flac/01-renamed"]
		if before := original[wrong]; renamed.ID == "" || renamed.ID != before.ID || renamed.Fingerprint == "" || renamed.Fingerprint != before.Fingerprint {
			t.Errorf("改名改变曲目或指纹：之前 %+v，之后 %+v", before, renamed)
		}
		if cached["Flac/01-renamed"].ParserVersion != parser.CacheVersion(".flac") || cached[extensionless].SeenScanID != rewritten.ID {
			t.Errorf("改名后真实格式或 CUE 父缓存标记错误")
		}
		requireScanReadArtwork(t, fixture, service, root, extensionless, cover)
	})
	t.Run("unknown_format_does_not_open_until_source_changes", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		root := insertScannerRoot(t, fixture, "unknown-format")
		service, logs := newIncrementalTestService(t, fixture)
		const relative = "Album/known.aac"
		path := filepath.Join(root.Path, relative)
		writeAudio(t, root, relative, audioFile{seed: 15, title: "Identify again", album: "Synthetic", track: 1})
		valid, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		initialTime := time.Unix(1700000000, 123456000)
		setScanReadTime(t, path, initialTime)
		runIncrementalTestScan(t, service, logs, root)
		old := readIncrementalTestTracks(t, fixture, root)[relative]

		unknown := bytes.Repeat([]byte{0x61}, len(valid))
		writeFixtureFile(t, root, relative, unknown)
		setScanReadTime(t, path, initialTime.Add(time.Microsecond))
		opens := scanReadOpenCounter(t, path)
		runUnknown := func(wantOpen bool, format string) {
			t.Helper()
			scan := runFilterLifecycleScan(t, service)
			requireFilterScan(t, scan, "succeeded", 1)
			requireDiagnosticCount(t, fixture, scan.ID, "unsupported_format", 1)
			requireDiagnosticCount(t, fixture, scan.ID, "parse_failure", 0)
			if n := opens()[path]; (n > 0) != wantOpen {
				t.Errorf("判型入口打开 %d 次，期望有打开 = %t", n, wantOpen)
			}
			summary := logs.summary(t, scan.ID, root.ID)
			if wantOpen && (summary.FilesParsed != 1 || summary.ParseCacheWrites != 1 || summary.ParseCacheHits != 0) ||
				!wantOpen && (summary.FilesParsed != 0 || summary.ParseCacheWrites != 0 || summary.ParseCacheHits != 1) {
				t.Errorf("未知格式扫描统计不符：%+v", summary)
			}
			entry := readIncrementalTestCache(t, fixture, root)[relative]
			if entry.ParserVersion != format || entry.SeenScanID != scan.ID || entry.Observation.Format != "" {
				t.Errorf("未知格式缓存的版本/seen/真实格式 = %q/%q/%q，期望 %q/%q/空", entry.ParserVersion, entry.SeenScanID, entry.Observation.Format, format, scan.ID)
			}
			if got := readIncrementalTestTracks(t, fixture, root)[relative]; got != old || got.ID == "" {
				t.Errorf("未知格式应保住现存来源：之前 %+v，之后 %+v", old, got)
			}
		}
		const version = "c6.unknown"
		runUnknown(true, version)
		runUnknown(false, version)
		writeFixtureFile(t, root, relative, unknown[:len(unknown)-1])
		setScanReadTime(t, path, initialTime.Add(time.Microsecond))
		_ = opens()
		runUnknown(true, version)
		writeFixtureFile(t, root, relative, valid[:len(valid)-1])
		setScanReadTime(t, path, initialTime.Add(2*time.Microsecond))
		_ = opens()
		runIncrementalTestScan(t, service, logs, root)
		if n := opens()[path]; n == 0 {
			t.Error("同大小文件的微秒 mtime 改变后未重新打开")
		}
		entry := readIncrementalTestCache(t, fixture, root)[relative]
		if entry.Observation.Format != ".flac" || entry.ParserVersion != parser.CacheVersion(".flac") {
			t.Errorf("微秒 mtime 改变后真实格式 = %q/%q", entry.Observation.Format, entry.ParserVersion)
		}
		requireTrackAt(t, presentTrackIDs(t, fixture, root.ID), relative, old.ID)
	})
}

func requireScanReadArtwork(t *testing.T, f *libraryFixture, service *Service, root registeredRoot, physical string, want []byte) {
	t.Helper()
	var source, mime, key string
	err := f.database.QueryRowContext(t.Context(), `SELECT DISTINCT a.source_type,a.mime_type,a.storage_key FROM release_artworks a
		JOIN media m ON m.release_id=a.release_id JOIN tracks t ON t.medium_id=m.id
		WHERE t.source_root_id=$1 AND (t.relative_path=$2 OR t.cue_parent_relative_path=$2) AND t.source_status='present'`, root.ID, physical).Scan(&source, &mime, &key)
	if err != nil {
		t.Fatalf("读取 %s 的封面：%v", physical, err)
	}
	got, err := os.ReadFile(filepath.Join(service.dataDirectory, "artwork", key))
	if err != nil || source != "embedded" || mime != "image/png" || !bytes.Equal(got, want) {
		t.Errorf("%s 的封面来源/MIME/内容/错误为 %s/%s/%x/%v，期望内嵌 PNG %x", physical, source, mime, got, err, want)
	}
}

func setScanReadTime(t *testing.T, path string, instant time.Time) {
	t.Helper()
	if err := os.Chtimes(path, instant, instant); err != nil {
		t.Fatal(err)
	}
}

type scanReadTrackSnapshot struct {
	ID, ReleaseID, Title, SourceStatus, SourceIdentity string
	Disc, Position                                     int
	Tags                                               string
}

func readScanReadTrackSnapshots(t *testing.T, f *libraryFixture, root registeredRoot) map[string]scanReadTrackSnapshot {
	t.Helper()
	rows, err := f.database.QueryContext(t.Context(), `SELECT t.relative_path,t.id::text,m.release_id::text,t.title,t.source_status,
		COALESCE(t.source_identity,''),t.disc_number,t.position,COALESCE(t.scan_tags::text,'')
		FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.source_root_id=$1 ORDER BY t.source_status`, root.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tracks := map[string]scanReadTrackSnapshot{}
	for rows.Next() {
		var path string
		var track scanReadTrackSnapshot
		if err := rows.Scan(&path, &track.ID, &track.ReleaseID, &track.Title, &track.SourceStatus, &track.SourceIdentity, &track.Disc, &track.Position, &track.Tags); err != nil {
			t.Fatal(err)
		}
		tracks[path] = track
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tracks
}

// 独占租约把扫描器的 open 阻塞在前一次 Stat 之后，收到 SIGIO 再用已打开的
// 描述符写入同长度内容。释放租约后扫描器读到新值，随后才能执行读后 Stat。
func scanReadWhileOpening(t *testing.T, path string, rewrite []byte, read func(context.Context) error) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(rewrite)) != before.Size() {
		t.Fatalf("同步写入必须保持大小：%d 与 %d", len(rewrite), before.Size())
	}
	opening := make(chan os.Signal, 1)
	signal.Notify(opening, syscall.SIGIO)
	defer signal.Stop(opening)
	fd := f.Fd()
	if _, err := unix.FcntlInt(fd, unix.F_SETLEASE, unix.F_WRLCK); err != nil {
		t.Fatalf("设置文件租约：%v", err)
	}
	defer unix.FcntlInt(fd, unix.F_SETLEASE, unix.F_UNLCK)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- read(ctx)
	}()
	select {
	case <-opening:
	case err := <-done:
		t.Fatalf("扫描在文件打开同步点前结束：%v", err)
	case <-ctx.Done():
		t.Fatalf("等待扫描器打开 %s：%v", path, ctx.Err())
	}
	if _, err := f.WriteAt(rewrite, 0); err != nil {
		t.Fatal(err)
	}
	setScanReadTime(t, path, before.ModTime().Add(2*time.Microsecond))
	if _, err := unix.FcntlInt(fd, unix.F_SETLEASE, unix.F_UNLCK); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func scanReadStartAndWait(service *Service, scanID *string) func(context.Context) error {
	return func(ctx context.Context) error {
		started, err := service.StartScan(ctx)
		if err != nil {
			return err
		}
		if !started.Started {
			return errors.New("扫描未启动")
		}
		*scanID = started.Scan.ID
		return service.WaitForScans(ctx)
	}
}

func TestPostgreSQLScanReadFileChanged(t *testing.T) {
	for _, tc := range []struct {
		name, relative, parent, sheet string
		old                           bool
	}{
		{"existing_physical", "Album/01.mp3", "", "", true},
		{"new_physical", "Album/01.mp3", "", "", false},
		{"cue_text", "Album/00.cue", "Album/01.mp3", "Album/00.cue", true},
		{"cue_before_parent", "Album/01.mp3", "Album/01.mp3", "Album/00.cue", true},
		{"parent_before_cue", "Album/01.mp3", "Album/01.mp3", "Album/02.cue", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			root := insertScannerRoot(t, fixture, "changed-source")
			service, logs := newIncrementalTestService(t, fixture)
			physical := tc.parent
			if physical == "" {
				physical = tc.relative
			}
			cover := pngArtworkOfWidth(9)
			path := filepath.Join(root.Path, tc.relative)
			writeIncrementalTestMP3(t, root, physical, 90, 1, cover, [2]string{"COMMENT", "old comment"})
			if tc.sheet != "" {
				doc := bytes.Replace(filterCueDocument(filepath.Base(physical)), []byte("00:01:00"), []byte("00:00:05"), 1)
				writeFixtureFile(t, root, tc.sheet, doc)
			}
			oldTime := time.Unix(1700000000, 123456000)
			setScanReadTime(t, path, oldTime)
			if tc.old {
				runIncrementalTestScan(t, service, logs, root)
			}
			before := readScanReadTrackSnapshots(t, fixture, root)
			wantOldTracks := 1
			if tc.sheet != "" {
				wantOldTracks = 2
			}
			if tc.old && len(before) != wantOldTracks {
				t.Fatalf("初始曲目数 = %d，期望 %d：%+v", len(before), wantOldTracks, before)
			}
			if tc.old {
				requireScanReadArtwork(t, fixture, service, root, physical, cover)
			}
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			newRead := bytes.Replace(original, []byte("old comment"), []byte("new comment"), 1)
			newRead = bytes.Replace(newRead, cover, pngArtworkOfWidth(8), 1)
			if tc.relative == tc.sheet {
				newRead = bytes.Replace(original, []byte("First"), []byte("Newer"), 1)
			}
			if bytes.Equal(newRead, original) {
				t.Fatal("夹具缺少预期的可替换标签")
			}
			setScanReadTime(t, path, oldTime.Add(time.Second))
			writeAudio(t, root, "Other/01.flac", audioFile{seed: 98, title: "Stable", album: "Other album", track: 1})
			var scanID string
			scanReadWhileOpening(t, path, newRead, scanReadStartAndWait(service, &scanID))
			scan, err := service.GetScan(t.Context(), scanID)
			if err != nil {
				t.Fatal(err)
			}
			wantSkipped := int64(1)
			if tc.parent == tc.relative {
				wantSkipped = 2
			}
			requireFilterScan(t, scan, "succeeded", wantSkipped)
			requireDiagnosticCount(t, fixture, scan.ID, "parse_failure", 0)
			after := readScanReadTrackSnapshots(t, fixture, root)
			if stable := after["Other/01.flac"]; stable.Title != "Stable" || stable.SourceStatus != "present" {
				t.Errorf("同轮稳定文件未入库：%+v", stable)
			}
			delete(after, "Other/01.flac")
			if !reflect.DeepEqual(before, after) {
				t.Errorf("读中变化应保留旧曲目和标签且不创建新轨：之前 %+v，之后 %+v", before, after)
			}
			if tc.relative != tc.sheet {
				if _, ok := readIncrementalTestCache(t, fixture, root)[physical]; ok {
					t.Errorf("变化的音频 %s 仍有解析缓存", physical)
				}
			}
			if tc.old {
				requireScanReadArtwork(t, fixture, service, root, physical, cover)
			}
			runIncrementalTestScan(t, service, logs, root)
			newTracks := readScanReadTrackSnapshots(t, fixture, root)
			delete(newTracks, "Other/01.flac")
			if len(newTracks) != wantOldTracks {
				t.Errorf("下次稳定读取后的曲目数 = %d，期望 %d", len(newTracks), wantOldTracks)
			}
			var hasNewTitle bool
			for name, track := range newTracks {
				if tc.old && track.ID != before[name].ID {
					t.Errorf("下次稳定读取替换旧曲目 %s：%+v", name, track)
				}
				if tc.relative == tc.sheet {
					hasNewTitle = hasNewTitle || track.Title == "Newer"
				} else if !strings.Contains(track.Tags, "new comment") || strings.Contains(track.Tags, "old comment") {
					t.Errorf("下次稳定读取未完整替换标签 %s：%s", name, track.Tags)
				}
			}
			if tc.relative == tc.sheet {
				if !hasNewTitle {
					t.Error("下次扫描缺少更新后的 CUE 曲名")
				}
				requireScanReadArtwork(t, fixture, service, root, physical, cover)
			} else {
				requireScanReadArtwork(t, fixture, service, root, physical, pngArtworkOfWidth(8))
			}
		})
	}
	t.Run("supplemental_fingerprint", testScanReadFingerprintChange)
}

type scanReadFingerprintGate struct {
	rootFingerprinter
	target string
	ready  chan struct{}
	resume chan struct{}
	hits   atomic.Int32
}

func (g *scanReadFingerprintGate) FingerprintObserved(ctx context.Context, relative string, size int64, modified time.Time) (string, error) {
	if relative != g.target {
		return g.rootFingerprinter.FingerprintObserved(ctx, relative, size, modified)
	}
	if g.hits.Add(1) != 1 {
		return "", fmt.Errorf("已丢弃的暂存来源 %s 重复读指纹", relative)
	}
	close(g.ready)
	select {
	case <-g.resume:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return g.rootFingerprinter.FingerprintObserved(ctx, relative, size, modified)
}

func testScanReadFingerprintChange(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "changed-fingerprint")
	service, logs := newIncrementalTestService(t, fixture)
	const relative = "Album/01.flac"
	path := filepath.Join(root.Path, relative)
	writeAudio(t, root, relative, audioFile{seed: 90, title: "Old title", album: "Album", track: 1, zeroMD5: true})
	setScanReadTime(t, path, time.Unix(1700000000, 0))
	runIncrementalTestScan(t, service, logs, root)
	before := readScanReadTrackSnapshots(t, fixture, root)[relative]
	writeAudio(t, root, relative, audioFile{seed: 90, title: "Mid title", album: "Album", track: 1, zeroMD5: true})
	mtime := time.Unix(1700000001, 0)
	setScanReadTime(t, path, mtime)
	writeAudio(t, root, "Album/02.flac", audioFile{seed: 91, title: "Stable", album: "Album", track: 2})
	gate := &scanReadFingerprintGate{rootFingerprinter: rootFingerprinter{root: root}, target: relative, ready: make(chan struct{}), resume: make(chan struct{})}
	service.fingerprinter = func(registeredRoot) ingest.Fingerprinter { return gate }
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	resume := sync.OnceFunc(func() { close(gate.resume) })
	defer resume()
	started, err := service.StartScan(ctx)
	if err != nil || !started.Started {
		t.Fatalf("启动指纹扫描：%+v/%v", started, err)
	}
	select {
	case <-gate.ready:
	case <-ctx.Done():
		t.Fatal("未到达补读指纹的同步点")
	}
	mid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	newRead := bytes.Replace(mid, []byte("Mid title"), []byte("New title"), 1)
	if bytes.Equal(mid, newRead) {
		t.Fatal("指纹夹具缺少待替换标题")
	}
	scanReadWhileOpening(t, path, newRead, func(ctx context.Context) error {
		resume()
		return service.WaitForScans(ctx)
	})
	service.fingerprinter = nil
	scan, err := service.GetScan(t.Context(), started.Scan.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireFilterScan(t, scan, "succeeded", 1)
	after := readScanReadTrackSnapshots(t, fixture, root)
	if after[relative] != before || after["Album/02.flac"].Title != "Stable" {
		t.Errorf("指纹补读时变化的轨未保住或同候选的正常轨未保存：之前 %+v，之后 %+v", before, after)
	}
	if _, ok := readIncrementalTestCache(t, fixture, root)[relative]; ok {
		t.Error("指纹补读发现变化后仍保留解析缓存")
	}
	runIncrementalTestScan(t, service, logs, root)
	if track := readScanReadTrackSnapshots(t, fixture, root)[relative]; track.Title != "New title" || track.ID != before.ID {
		t.Errorf("下次稳定读的曲目 = %+v，期望保留编号和新标题", track)
	}
}
