package library

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// audioFile 描述一个合成 FLAC：seed 决定音频内容（STREAMINFO 的 MD5 与帧字节），其余是标签。
type audioFile struct {
	seed    byte
	zeroMD5 bool // MD5 全零：指纹要靠读帧数据计算，走 Fingerprinter 端口
	title   string
	album   string
	track   int
}

func writeAudio(t *testing.T, root registeredRoot, relativePath string, file audioFile) {
	t.Helper()
	var md5 [16]byte
	if !file.zeroMD5 {
		for index := range md5 {
			md5[index] = file.seed + byte(index) + 1
		}
	}
	comments := []string{"TITLE=" + file.title, "ARTIST=Artist", "ALBUM=" + file.album, "TRACKNUMBER=" + strconv.Itoa(file.track)}
	writeFixtureFile(t, root, relativePath, parsertest.FLACWithAudio(t, md5, bytes.Repeat([]byte{file.seed}, 64), comments...))
}

func writeFixtureFile(t *testing.T, root registeredRoot, relativePath string, data []byte) {
	t.Helper()
	path := filepath.Join(root.Path, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
}

func moveFixture(t *testing.T, root registeredRoot, from, to string) {
	t.Helper()
	target := filepath.Join(root.Path, filepath.FromSlash(to))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create fixture target directory: %v", err)
	}
	if err := os.Rename(filepath.Join(root.Path, filepath.FromSlash(from)), target); err != nil {
		t.Fatalf("move fixture: %v", err)
	}
}

type trackRow struct {
	ID, Path, Status string
	Bound            bool // 仍持有来源身份
	Fingerprint      string
}

func readTracks(t *testing.T, fixture *libraryFixture, rootID string) []trackRow {
	t.Helper()
	rows, err := fixture.database.Query(`SELECT id::text,relative_path,source_status,source_identity IS NOT NULL,COALESCE(content_fingerprint,'')
		FROM tracks WHERE source_root_id=$1::uuid ORDER BY relative_path,id`, rootID)
	if err != nil {
		t.Fatalf("read tracks: %v", err)
	}
	defer rows.Close()
	var tracks []trackRow
	for rows.Next() {
		var row trackRow
		if err := rows.Scan(&row.ID, &row.Path, &row.Status, &row.Bound, &row.Fingerprint); err != nil {
			t.Fatalf("scan track: %v", err)
		}
		tracks = append(tracks, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tracks: %v", err)
	}
	return tracks
}

// presentTrackIDs 返回 root 中 present 曲目的 路径 → ID。
func presentTrackIDs(t *testing.T, fixture *libraryFixture, rootID string) map[string]string {
	t.Helper()
	ids := map[string]string{}
	for _, track := range readTracks(t, fixture, rootID) {
		if track.Status == "present" {
			ids[track.Path] = track.ID
		}
	}
	return ids
}

func requireTrackAt(t *testing.T, ids map[string]string, path, wantID string) {
	t.Helper()
	if ids[path] != wantID {
		t.Fatalf("路径 %q 上的曲目 = %q，期望沿用 %q（现有 %v）", path, ids[path], wantID, ids)
	}
}

func requireNoMissingTracks(t *testing.T, fixture *libraryFixture, rootID string) {
	t.Helper()
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_status='missing'`, 0, rootID)
}

func requireDiagnosticCount(t *testing.T, fixture *libraryFixture, scanID, kind string, want int) {
	t.Helper()
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_diagnostics WHERE scan_run_id=$1::uuid AND kind=$2`, want, scanID, kind)
}

type testScanResult struct {
	ID     string
	Status string
}

// runTestScan 按生产 runScan 的顺序扫描 roots：逐个 scanRoot，再按结果 finalizeScan。
func runTestScan(t *testing.T, fixture *libraryFixture, service *Service, roots ...registeredRoot) testScanResult {
	t.Helper()
	ctx := context.Background()
	scanID := insertRunningScan(t, fixture)
	connection, err := fixture.database.Conn(ctx)
	if err != nil {
		t.Fatalf("open scan connection: %v", err)
	}
	defer connection.Close()
	outcome := scanOutcome{Complete: true}
	rootIDs := make([]string, 0, len(roots))
	for _, root := range roots {
		rootIDs = append(rootIDs, root.ID)
		if scanErr := service.scanRoot(ctx, connection, scanID, root); scanErr != nil {
			outcome.Complete = false
		}
	}
	status, err := service.finalizeScan(ctx, connection, scanID, rootIDs, outcome)
	if err != nil {
		t.Fatalf("finalize scan: %v", err)
	}
	return testScanResult{ID: scanID, Status: status}
}

func requireScanStatus(t *testing.T, scan testScanResult, want string) {
	t.Helper()
	if scan.Status != want {
		t.Fatalf("扫描终态 = %q，期望 %q", scan.Status, want)
	}
}

func TestPostgreSQLRescanKeepsTrackIdentity(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "rescan-identity")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "One", album: "Album", track: 1})
	writeAudio(t, root, "Album/02.flac", audioFile{seed: 2, title: "Two", album: "Album", track: 2, zeroMD5: true})

	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := presentTrackIDs(t, fixture, root.ID)
	if len(before) != 2 {
		t.Fatalf("首次扫描的曲目 = %v", before)
	}
	releasesBefore := releaseIDsByTitle(t, fixture, root.ID)
	for _, track := range readTracks(t, fixture, root.ID) {
		if track.Fingerprint == "" {
			t.Fatalf("曲目 %q 没有保存指纹", track.Path)
		}
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_size IS NOT NULL AND source_modified_at IS NOT NULL`, 2, root.ID)

	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	after := presentTrackIDs(t, fixture, root.ID)
	for path, id := range before {
		requireTrackAt(t, after, path, id)
	}
	requireSameReleases(t, releasesBefore, releaseIDsByTitle(t, fixture, root.ID))
	requireNoMissingTracks(t, fixture, root.ID)
}

func TestPostgreSQLDirectoryRenameRebindsTracks(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "directory-rename")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "One", album: "Album", track: 1})
	writeAudio(t, root, "Album/02.flac", audioFile{seed: 2, title: "Two", album: "Album", track: 2, zeroMD5: true})
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := presentTrackIDs(t, fixture, root.ID)
	releasesBefore := releaseIDsByTitle(t, fixture, root.ID)

	moveFixture(t, root, "Album", "Renamed Album")
	scan := runTestScan(t, fixture, service, root)
	requireScanStatus(t, scan, "succeeded")
	after := presentTrackIDs(t, fixture, root.ID)
	requireTrackAt(t, after, "Renamed Album/01.flac", before["Album/01.flac"])
	requireTrackAt(t, after, "Renamed Album/02.flac", before["Album/02.flac"])
	requireSameReleases(t, releasesBefore, releaseIDsByTitle(t, fixture, root.ID))
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 2, root.ID)
	requireNoMissingTracks(t, fixture, root.ID)
	requireDiagnosticCount(t, fixture, scan.ID, "identity_ambiguous", 0)
}

func TestPostgreSQLMovedFileKeepsTrackIdentity(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "moved-file")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "First/01.flac", audioFile{seed: 1, title: "One", album: "First", track: 1})
	writeAudio(t, root, "First/02.flac", audioFile{seed: 2, title: "Two", album: "First", track: 2})
	writeAudio(t, root, "Second/01.flac", audioFile{seed: 3, title: "Three", album: "Second", track: 1})
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := presentTrackIDs(t, fixture, root.ID)

	// 同一个 root 内移到另一个专辑目录，并顺带改名。
	moveFixture(t, root, "First/02.flac", "Second/02 Moved.flac")
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	after := presentTrackIDs(t, fixture, root.ID)
	requireTrackAt(t, after, "Second/02 Moved.flac", before["First/02.flac"])
	requireTrackAt(t, after, "First/01.flac", before["First/01.flac"])
	requireTrackAt(t, after, "Second/01.flac", before["Second/01.flac"])
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 3, root.ID)
	requireNoMissingTracks(t, fixture, root.ID)
}

func TestPostgreSQLSwappedFilesFollowContent(t *testing.T) {
	t.Run("two files exchange names", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		root := insertScannerRoot(t, fixture, "swap-two")
		service := scannerTestApplication(fixture)
		writeAudio(t, root, "Album/a.flac", audioFile{seed: 1, title: "First", album: "Album", track: 1})
		writeAudio(t, root, "Album/b.flac", audioFile{seed: 2, title: "Second", album: "Album", track: 2})
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		before := presentTrackIDs(t, fixture, root.ID)

		moveFixture(t, root, "Album/a.flac", "Album/tmp.flac")
		moveFixture(t, root, "Album/b.flac", "Album/a.flac")
		moveFixture(t, root, "Album/tmp.flac", "Album/b.flac")
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		after := presentTrackIDs(t, fixture, root.ID)
		requireTrackAt(t, after, "Album/b.flac", before["Album/a.flac"])
		requireTrackAt(t, after, "Album/a.flac", before["Album/b.flac"])
		assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 2, root.ID)
		requireNoMissingTracks(t, fixture, root.ID)
		var title string
		if err := fixture.database.QueryRow(`SELECT title FROM tracks WHERE id=$1::uuid`, before["Album/a.flac"]).Scan(&title); err != nil || title != "First" {
			t.Fatalf("跟着内容走的曲目标题 = %q（错误 %v），期望 First", title, err)
		}
	})

	t.Run("three files rotate", func(t *testing.T) {
		fixture := newLibraryFixture(t)
		root := insertScannerRoot(t, fixture, "swap-rotate")
		service := scannerTestApplication(fixture)
		for index, name := range []string{"a", "b", "c"} {
			writeAudio(t, root, "Album/"+name+".flac", audioFile{seed: byte(index + 1), title: strings.ToUpper(name), album: "Album", track: index + 1})
		}
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		before := presentTrackIDs(t, fixture, root.ID)

		// a→b、b→c、c→a：处理顺序无论如何都要先腾出再占用。
		moveFixture(t, root, "Album/c.flac", "Album/tmp.flac")
		moveFixture(t, root, "Album/b.flac", "Album/c.flac")
		moveFixture(t, root, "Album/a.flac", "Album/b.flac")
		moveFixture(t, root, "Album/tmp.flac", "Album/a.flac")
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		after := presentTrackIDs(t, fixture, root.ID)
		requireTrackAt(t, after, "Album/b.flac", before["Album/a.flac"])
		requireTrackAt(t, after, "Album/c.flac", before["Album/b.flac"])
		requireTrackAt(t, after, "Album/a.flac", before["Album/c.flac"])
		assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 3, root.ID)
		requireNoMissingTracks(t, fixture, root.ID)
	})
}

func TestPostgreSQLIdenticalAudioIsNotRebound(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "identical-audio")
	service := scannerTestApplication(fixture)
	// 两个文件的音频完全相同：指纹相同，不能唯一对应。
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 7, title: "Twin One", album: "Album", track: 1})
	writeAudio(t, root, "Album/02.flac", audioFile{seed: 7, title: "Twin Two", album: "Album", track: 2})
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := presentTrackIDs(t, fixture, root.ID)

	moveFixture(t, root, "Album", "Moved")
	scan := runTestScan(t, fixture, service, root)
	requireScanStatus(t, scan, "succeeded")
	after := presentTrackIDs(t, fixture, root.ID)
	for path, id := range after {
		for _, oldID := range before {
			if id == oldID {
				t.Fatalf("内容相同的文件被重绑定：%q 沿用了 %q", path, id)
			}
		}
	}
	if len(after) != 2 {
		t.Fatalf("新位置的曲目 = %v", after)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_status='missing'`, 2, root.ID)
	requireDiagnosticCount(t, fixture, scan.ID, "identity_ambiguous", 1)
}

func TestPostgreSQLIncompleteScanDefersMovesUntilCompleteScan(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "incomplete-defers")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "One", album: "Album", track: 1})
	writeAudio(t, root, "Album/02.flac", audioFile{seed: 2, title: "Two", album: "Album", track: 2})
	writeAudio(t, root, "Other/01.flac", audioFile{seed: 3, title: "Three", album: "Other", track: 1})
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := presentTrackIDs(t, fixture, root.ID)

	// 目录改名的同时出现一个损坏文件，使这次遍历不完整；另一个目录里有真正的新文件。
	moveFixture(t, root, "Album", "Renamed")
	writeFixtureFile(t, root, "Broken/bad.mp3", []byte("not-an-mp3"))
	writeAudio(t, root, "Fresh/01.flac", audioFile{seed: 9, title: "Fresh", album: "Fresh", track: 1})
	incomplete := runTestScan(t, fixture, service, root)
	requireScanStatus(t, incomplete, "incomplete")
	deferred := presentTrackIDs(t, fixture, root.ID)
	for _, path := range []string{"Album/01.flac", "Album/02.flac", "Other/01.flac"} {
		requireTrackAt(t, deferred, path, before[path])
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND relative_path LIKE 'Renamed/%'`, 0, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND relative_path='Fresh/01.flac' AND source_status='present'`, 1, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 4, root.ID)
	requireNoMissingTracks(t, fixture, root.ID)
	requireDiagnosticCount(t, fixture, incomplete.ID, "identity_deferred", 1)

	if err := os.RemoveAll(filepath.Join(root.Path, "Broken")); err != nil {
		t.Fatalf("remove broken fixture: %v", err)
	}
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	after := presentTrackIDs(t, fixture, root.ID)
	requireTrackAt(t, after, "Renamed/01.flac", before["Album/01.flac"])
	requireTrackAt(t, after, "Renamed/02.flac", before["Album/02.flac"])
	requireTrackAt(t, after, "Other/01.flac", before["Other/01.flac"])
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 4, root.ID)
	requireNoMissingTracks(t, fixture, root.ID)
}

// cancelingFingerprinter 在第一次被调用时取消 context，模拟扫描在规划期间被取消。
type cancelingFingerprinter struct {
	inner  catalog.Fingerprinter
	cancel context.CancelFunc
}

func (fingerprinter cancelingFingerprinter) Fingerprint(ctx context.Context, relativePath string) (string, error) {
	fingerprint, err := fingerprinter.inner.Fingerprint(ctx, relativePath)
	fingerprinter.cancel()
	return fingerprint, err
}

func TestPostgreSQLCanceledScanDoesNotRebind(t *testing.T) {
	setup := func(t *testing.T) (*libraryFixture, registeredRoot, *Service, map[string]string) {
		fixture := newLibraryFixture(t)
		root := insertScannerRoot(t, fixture, "canceled-rebind")
		service := scannerTestApplication(fixture)
		// MD5 全零：指纹要靠读帧数据计算，规划期间才会调用 Fingerprinter。
		writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "One", album: "Album", track: 1, zeroMD5: true})
		writeAudio(t, root, "Album/02.flac", audioFile{seed: 2, title: "Two", album: "Album", track: 2, zeroMD5: true})
		requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
		return fixture, root, service, presentTrackIDs(t, fixture, root.ID)
	}
	verifyUnchanged := func(t *testing.T, fixture *libraryFixture, root registeredRoot, service *Service, scanID string, before map[string]string, scanErr error) {
		t.Helper()
		if scanErr == nil {
			t.Fatal("被取消的 root 被报告为完整")
		}
		connection, err := fixture.database.Conn(context.Background())
		if err != nil {
			t.Fatalf("open finalize connection: %v", err)
		}
		defer connection.Close()
		status, err := service.finalizeScan(context.Background(), connection, scanID, []string{root.ID}, scanOutcome{Canceled: true})
		if err != nil || status != "canceled" {
			t.Fatalf("终态 = %q（错误 %v），期望 canceled", status, err)
		}
		after := presentTrackIDs(t, fixture, root.ID)
		for path, id := range before {
			requireTrackAt(t, after, path, id)
		}
		assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 2, root.ID)
		requireNoMissingTracks(t, fixture, root.ID)
	}

	t.Run("canceled while planning", func(t *testing.T) {
		fixture, root, service, before := setup(t)
		moveFixture(t, root, "Album", "Renamed")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		service.fingerprinter = func(root registeredRoot) catalog.Fingerprinter {
			return cancelingFingerprinter{inner: rootFingerprinter{root: root}, cancel: cancel}
		}
		scanID := insertRunningScan(t, fixture)
		connection, err := fixture.database.Conn(context.Background())
		if err != nil {
			t.Fatalf("open scan connection: %v", err)
		}
		defer connection.Close()
		scanErr := service.scanRoot(ctx, connection, scanID, root)
		verifyUnchanged(t, fixture, root, service, scanID, before, scanErr)
		assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND relative_path LIKE 'Renamed/%'`, 0, root.ID)
	})

	t.Run("canceled before the walk", func(t *testing.T) {
		fixture, root, service, before := setup(t)
		moveFixture(t, root, "Album", "Renamed")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		scanID := insertRunningScan(t, fixture)
		connection, err := fixture.database.Conn(context.Background())
		if err != nil {
			t.Fatalf("open scan connection: %v", err)
		}
		defer connection.Close()
		verifyUnchanged(t, fixture, root, service, scanID, before, service.scanRoot(ctx, connection, scanID, root))
	})
}

func TestPostgreSQLCrossRootMoveGetsNewIdentity(t *testing.T) {
	fixture := newLibraryFixture(t)
	rootA := insertScannerRoot(t, fixture, "cross-root-a")
	rootB := insertScannerRoot(t, fixture, "cross-root-b")
	service := scannerTestApplication(fixture)
	writeAudio(t, rootA, "Album/01.flac", audioFile{seed: 1, title: "Mover", album: "Album", track: 1})
	writeAudio(t, rootB, "Other/01.flac", audioFile{seed: 2, title: "Stays", album: "Other", track: 1})
	requireScanStatus(t, runTestScan(t, fixture, service, rootA, rootB), "succeeded")
	oldID := presentTrackIDs(t, fixture, rootA.ID)["Album/01.flac"]

	target := filepath.Join(rootB.Path, "Album", "01.flac")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create target directory: %v", err)
	}
	if err := os.Rename(filepath.Join(rootA.Path, "Album", "01.flac"), target); err != nil {
		t.Fatalf("move across roots: %v", err)
	}
	requireScanStatus(t, runTestScan(t, fixture, service, rootA, rootB), "succeeded")
	movedID := presentTrackIDs(t, fixture, rootB.ID)["Album/01.flac"]
	if movedID == "" || movedID == oldID {
		t.Fatalf("跨 root 移动后的曲目 = %q，必须是新身份（旧 %q）", movedID, oldID)
	}
	var status string
	if err := fixture.database.QueryRow(`SELECT source_status FROM tracks WHERE id=$1::uuid`, oldID).Scan(&status); err != nil || status != "missing" {
		t.Fatalf("旧 root 中的旧曲目状态 = %q（错误 %v），期望 missing", status, err)
	}
}

func TestPostgreSQLReplacedContentGetsNewIdentity(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "replaced-content")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "Original", album: "Album", track: 1})
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	oldID := presentTrackIDs(t, fixture, root.ID)["Album/01.flac"]

	// 原地换成另一份音频（例如重新抓轨）：同一路径不能证明内容相同。
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 2, title: "Replacement", album: "Album", track: 1})
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(root.Path, "Album", "01.flac"), future, future); err != nil {
		t.Fatalf("change modification time: %v", err)
	}
	scan := runTestScan(t, fixture, service, root)
	requireScanStatus(t, scan, "succeeded")

	tracks := readTracks(t, fixture, root.ID)
	if len(tracks) != 2 {
		t.Fatalf("曲目 = %+v，期望新旧各一条", tracks)
	}
	for _, track := range tracks {
		switch track.ID {
		case oldID:
			if track.Status != "missing" || track.Bound {
				t.Fatalf("被替换的旧曲目 = %+v，期望 missing 且失去来源身份", track)
			}
		default:
			if track.Status != "present" || !track.Bound || track.Path != "Album/01.flac" {
				t.Fatalf("新曲目 = %+v，期望 present 并持有来源身份", track)
			}
		}
	}
	requireDiagnosticCount(t, fixture, scan.ID, "content_replaced", 1)
}

func TestPostgreSQLCueDirectoryRenameRebindsVirtualTracks(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "cue-directory-rename")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Album/image.flac", audioFile{seed: 5, title: "Image", album: "Cue Release", track: 1})
	cue := strings.Join([]string{
		`TITLE "Cue Release"`,
		`PERFORMER "Artist"`,
		`FILE "image.flac" FLAC`,
		`  TRACK 01 AUDIO`,
		`    TITLE "First"`,
		`    INDEX 01 00:00:00`,
		`  TRACK 02 AUDIO`,
		`    TITLE "Second"`,
		`    INDEX 01 00:01:00`,
	}, "\n")
	writeFixtureFile(t, root, "Album/album.cue", []byte(cue))
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	virtualIDs := func() map[int]string {
		rows, err := fixture.database.Query(`SELECT cue_index_frames,id::text FROM tracks WHERE source_root_id=$1::uuid AND source_kind='cue_virtual' AND source_status='present'`, root.ID)
		if err != nil {
			t.Fatalf("read virtual tracks: %v", err)
		}
		defer rows.Close()
		ids := map[int]string{}
		for rows.Next() {
			var frames int
			var id string
			if err := rows.Scan(&frames, &id); err != nil {
				t.Fatalf("scan virtual track: %v", err)
			}
			ids[frames] = id
		}
		return ids
	}
	before := virtualIDs()
	if len(before) != 2 {
		t.Fatalf("CUE 虚拟轨 = %v", before)
	}

	moveFixture(t, root, "Album", "Renamed")
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	after := virtualIDs()
	for frames, id := range before {
		if after[frames] != id {
			t.Fatalf("起点 %d 帧的虚拟轨 = %q，期望沿用 %q（现有 %v）", frames, after[frames], id, after)
		}
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND cue_sheet_path='Renamed/album.cue' AND cue_parent_relative_path='Renamed/image.flac'`, 2, root.ID)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 2, root.ID)
	requireNoMissingTracks(t, fixture, root.ID)
}

func TestPostgreSQLCompleteRootRebindsWhenAnotherRootIsUnavailable(t *testing.T) {
	fixture := newLibraryFixture(t)
	rootA := insertScannerRoot(t, fixture, "independent-a")
	rootB := insertScannerRoot(t, fixture, "independent-b")
	service := scannerTestApplication(fixture)
	writeAudio(t, rootA, "Album/01.flac", audioFile{seed: 1, title: "One", album: "Album", track: 1})
	writeAudio(t, rootB, "Album/01.flac", audioFile{seed: 2, title: "Two", album: "Album", track: 1})
	requireScanStatus(t, runTestScan(t, fixture, service, rootA, rootB), "succeeded")
	beforeA := presentTrackIDs(t, fixture, rootA.ID)
	beforeB := presentTrackIDs(t, fixture, rootB.ID)

	// 遍历完整性按 root 判断：B 不可用不能阻断 A 的改名处理，但整轮仍是 incomplete，不做 missing 对账。
	moveFixture(t, rootA, "Album", "Renamed")
	if err := os.RemoveAll(rootB.Path); err != nil {
		t.Fatalf("make root unavailable: %v", err)
	}
	requireScanStatus(t, runTestScan(t, fixture, service, rootA, rootB), "incomplete")
	afterA := presentTrackIDs(t, fixture, rootA.ID)
	requireTrackAt(t, afterA, "Renamed/01.flac", beforeA["Album/01.flac"])
	afterB := presentTrackIDs(t, fixture, rootB.ID)
	requireTrackAt(t, afterB, "Album/01.flac", beforeB["Album/01.flac"])
	requireNoMissingTracks(t, fixture, rootA.ID)
	requireNoMissingTracks(t, fixture, rootB.ID)
}

// recordingFingerprinter 统计对每个文件的指纹计算次数，并可让指定文件失败。
type recordingFingerprinter struct {
	inner    catalog.Fingerprinter
	calls    *atomic.Int64
	failPath string
}

func (fingerprinter recordingFingerprinter) Fingerprint(ctx context.Context, relativePath string) (string, error) {
	fingerprinter.calls.Add(1)
	if relativePath == fingerprinter.failPath {
		return "", errors.New("injected fingerprint failure")
	}
	return fingerprinter.inner.Fingerprint(ctx, relativePath)
}

func TestPostgreSQLUnchangedFilesReuseStoredFingerprints(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "reuse-fingerprints")
	service := scannerTestApplication(fixture)
	var calls atomic.Int64
	service.fingerprinter = func(root registeredRoot) catalog.Fingerprinter {
		return recordingFingerprinter{inner: rootFingerprinter{root: root}, calls: &calls}
	}
	// MD5 全零的文件没有免费指纹，只能经 Fingerprinter 计算。
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "One", album: "Album", track: 1, zeroMD5: true})
	writeAudio(t, root, "Album/02.flac", audioFile{seed: 2, title: "Two", album: "Album", track: 2, zeroMD5: true})

	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	if got := calls.Load(); got != 2 {
		t.Fatalf("首次扫描计算了 %d 次指纹，期望 2", got)
	}
	calls.Store(0)
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	if got := calls.Load(); got != 0 {
		t.Fatalf("文件没有变化的重扫计算了 %d 次指纹，期望 0", got)
	}

	// 只有内容与修改时间变了的文件才重新计算。
	writeAudio(t, root, "Album/02.flac", audioFile{seed: 3, title: "Two", album: "Album", track: 2, zeroMD5: true})
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(root.Path, "Album", "02.flac"), future, future); err != nil {
		t.Fatalf("change modification time: %v", err)
	}
	calls.Store(0)
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	if got := calls.Load(); got != 1 {
		t.Fatalf("只有一个文件变化的重扫计算了 %d 次指纹，期望 1", got)
	}
}

func TestPostgreSQLFingerprintFailureDefersScope(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "fingerprint-failure")
	service := scannerTestApplication(fixture)
	var calls atomic.Int64
	service.fingerprinter = func(root registeredRoot) catalog.Fingerprinter {
		return recordingFingerprinter{inner: rootFingerprinter{root: root}, calls: &calls, failPath: "Broken Album/01.flac"}
	}
	writeAudio(t, root, "Broken Album/01.flac", audioFile{seed: 1, title: "One", album: "Broken Album", track: 1, zeroMD5: true})
	writeAudio(t, root, "Good Album/01.flac", audioFile{seed: 2, title: "Two", album: "Good Album", track: 1, zeroMD5: true})

	scan := runTestScan(t, fixture, service, root)
	requireScanStatus(t, scan, "incomplete")
	requireDiagnosticCount(t, fixture, scan.ID, "fingerprint_failure", 1)
	ids := presentTrackIDs(t, fixture, root.ID)
	if len(ids) != 1 || ids["Good Album/01.flac"] == "" {
		t.Fatalf("指纹失败的 scope 应暂缓写入，其余照常写入：%v", ids)
	}

	// 问题修复后，下一次扫描写入被暂缓的 scope。
	service.fingerprinter = nil
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	if ids := presentTrackIDs(t, fixture, root.ID); len(ids) != 2 {
		t.Fatalf("修复后的曲目 = %v，期望两条", ids)
	}
}

func TestPostgreSQLSingleTrackCueMergedTrackKeepsIdentityOnRename(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "single-cue-rename")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Album/image.flac", audioFile{seed: 5, title: "Only Track", album: "Single Cue", track: 1})
	// 只有一个 TRACK 的 CUE：organizer 保留物理轨，并把 CUE 的区间事实合并进去。
	cue := strings.Join([]string{
		`TITLE "Single Cue"`,
		`PERFORMER "Artist"`,
		`FILE "image.flac" FLAC`,
		`  TRACK 01 AUDIO`,
		`    TITLE "Only Track"`,
		`    INDEX 01 00:00:00`,
	}, "\n")
	writeFixtureFile(t, root, "Album/album.cue", []byte(cue))
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := presentTrackIDs(t, fixture, root.ID)
	if len(before) != 1 || before["Album/image.flac"] == "" {
		t.Fatalf("单轨 CUE 应保留物理轨：%v", before)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_kind<>'cue_virtual' AND cue_index_frames IS NOT NULL`, 1, root.ID)

	moveFixture(t, root, "Album", "Renamed")
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	requireTrackAt(t, presentTrackIDs(t, fixture, root.ID), "Renamed/image.flac", before["Album/image.flac"])
	requireNoMissingTracks(t, fixture, root.ID)
}

func TestPostgreSQLScanAfterMigrationBackfillsFingerprintsWithoutChangingIdentity(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "backfill-fingerprints")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "One", album: "Album", track: 1})
	writeAudio(t, root, "Album/02.flac", audioFile{seed: 2, title: "Two", album: "Album", track: 2, zeroMD5: true})
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	before := presentTrackIDs(t, fixture, root.ID)

	// 迁移前保存的曲目没有指纹与文件状态。
	if _, err := fixture.database.Exec(`UPDATE tracks SET content_fingerprint=NULL,source_size=NULL,source_modified_at=NULL WHERE source_root_id=$1::uuid`, root.ID); err != nil {
		t.Fatalf("clear stored fingerprints: %v", err)
	}
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	after := presentTrackIDs(t, fixture, root.ID)
	for path, id := range before {
		requireTrackAt(t, after, path, id)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND content_fingerprint IS NOT NULL AND source_size IS NOT NULL AND source_modified_at IS NOT NULL`, 2, root.ID)
	requireNoMissingTracks(t, fixture, root.ID)
}

func TestPostgreSQLIncompleteScanDefersReplacedContent(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "incomplete-replaced")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "Original", album: "Album", track: 1})
	requireScanStatus(t, runTestScan(t, fixture, service, root), "succeeded")
	oldID := presentTrackIDs(t, fixture, root.ID)["Album/01.flac"]

	writeAudio(t, root, "Album/01.flac", audioFile{seed: 2, title: "Replacement", album: "Album", track: 1})
	writeFixtureFile(t, root, "Broken/bad.mp3", []byte("not-an-mp3"))
	incomplete := runTestScan(t, fixture, service, root)
	requireScanStatus(t, incomplete, "incomplete")
	if ids := presentTrackIDs(t, fixture, root.ID); len(ids) != 1 || ids["Album/01.flac"] != oldID {
		t.Fatalf("不完整遍历中换内容的 scope 应暂缓，旧曲目保持原状：%v", ids)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 1, root.ID)
	requireDiagnosticCount(t, fixture, incomplete.ID, "identity_deferred", 1)

	if err := os.RemoveAll(filepath.Join(root.Path, "Broken")); err != nil {
		t.Fatalf("remove broken fixture: %v", err)
	}
	complete := runTestScan(t, fixture, service, root)
	requireScanStatus(t, complete, "succeeded")
	requireDiagnosticCount(t, fixture, complete.ID, "content_replaced", 1)
	if newID := presentTrackIDs(t, fixture, root.ID)["Album/01.flac"]; newID == "" || newID == oldID {
		t.Fatalf("完整遍历后新内容应得到新曲目：%q（旧 %q）", newID, oldID)
	}
}

func TestPostgreSQLTrackPathUniquenessOnlyConstrainsBoundRows(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "path-uniqueness")
	groupID, releaseID, mediumID := uuid.New(), uuid.New(), uuid.New()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO release_groups(id) VALUES($1::uuid)`, []any{groupID}},
		{`INSERT INTO releases(id,group_id,title,artist,source_root_id) VALUES($1::uuid,$2::uuid,'Release','Artist',$3::uuid)`, []any{releaseID, groupID, root.ID}},
		{`INSERT INTO media(id,release_id,position) VALUES($1::uuid,$2::uuid,1)`, []any{mediumID, releaseID}},
	} {
		if _, err := fixture.database.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("prepare release graph: %v", err)
		}
	}
	insertTrack := func(identity any, fingerprint any) error {
		_, err := fixture.database.Exec(`INSERT INTO tracks(id,medium_id,position,title,artist,disc_number,source_root_id,relative_path,source_status,observed_at,source_kind,source_identity,content_fingerprint)
			VALUES($1::uuid,$2::uuid,1,'Title','Artist',1,$3::uuid,'Album/01.flac','present',NOW(),'physical',$4,$5)`, uuid.New(), mediumID, root.ID, identity, fingerprint)
		return err
	}

	if err := insertTrack("identity-one", nil); err != nil {
		t.Fatalf("insert bound track: %v", err)
	}
	// 失去来源身份的旧曲目可以与同一路径上的新曲目并存。
	if err := insertTrack(nil, nil); err != nil {
		t.Fatalf("insert unbound track at the same path: %v", err)
	}
	if err := insertTrack(nil, nil); err != nil {
		t.Fatalf("insert second unbound track at the same path: %v", err)
	}
	// 两条仍持有来源身份的曲目不能占用同一路径。
	if err := insertTrack("identity-two", nil); err == nil {
		t.Fatal("同一路径上的第二条已绑定曲目被接受")
	}
	if err := insertTrack(nil, ""); err == nil {
		t.Fatal("空的内容指纹被接受")
	}
	if err := insertTrack(nil, strings.Repeat("x", 257)); err == nil {
		t.Fatal("超过 256 字节的内容指纹被接受")
	}
}
