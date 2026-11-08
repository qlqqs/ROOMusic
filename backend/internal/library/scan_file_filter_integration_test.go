package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runFilterLifecycleScan 经真实 worker 执行预计数、遍历和终态写入，避免绕过跳过计数与排除项快照。
func runFilterLifecycleScan(t *testing.T, service *Service) Scan {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started, err := service.StartScan(ctx)
	if err != nil || !started.Started {
		t.Fatalf("启动扫描：started=%v err=%v", started.Started, err)
	}
	if err := service.WaitForScans(ctx); err != nil {
		t.Fatalf("等待扫描结束：%v", err)
	}
	scan, err := service.GetScan(ctx, started.Scan.ID)
	if err != nil {
		t.Fatalf("读取扫描：%v", err)
	}
	return scan
}

func requireFilterScan(t *testing.T, scan Scan, status string, skipped int64) {
	t.Helper()
	if scan.Status != status {
		t.Errorf("扫描终态=%q，期望 %q（诊断 %+v）", scan.Status, status, scan.Diagnostics)
	}
	if scan.SkippedFiles == nil || *scan.SkippedFiles != skipped {
		t.Errorf("跳过计数=%v，期望 %d", scan.SkippedFiles, skipped)
	}
}

func removeFilterFixture(t *testing.T, root registeredRoot, relative string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root.Path, filepath.FromSlash(relative))); err != nil {
		t.Fatalf("删除合成来源：%v", err)
	}
}

func TestPostgreSQLScanLifecycleHeldFilesStillReconcilesDeletedSources(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "held-files")
	service := scannerTestApplication(fixture)
	for index, name := range []string{"damaged", "deleted", "unchanged"} {
		writeAudio(t, root, "Album/"+name+".flac", audioFile{seed: byte(index + 1), title: name, album: "Album", track: index + 1})
	}
	requireFilterScan(t, runFilterLifecycleScan(t, service), "succeeded", 0)
	before := presentTrackIDs(t, fixture, root.ID)
	writeFixtureFile(t, root, "Album/._x.flac", []byte("ignored"))
	writeFixtureFile(t, root, "Album/unsupported.ape", []byte("unsupported"))
	writeFixtureFile(t, root, "Album/damaged.flac", []byte("damaged"))
	removeFilterFixture(t, root, "Album/deleted.flac")

	scan := runFilterLifecycleScan(t, service)
	requireFilterScan(t, scan, "succeeded", 2)
	after := presentTrackIDs(t, fixture, root.ID)
	for _, relative := range []string{"Album/damaged.flac", "Album/unchanged.flac"} {
		requireTrackAt(t, after, relative, before[relative])
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE id=$1::uuid AND source_status='missing'`, 1, before["Album/deleted.flac"])
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE id=$1::uuid AND title='damaged' AND content_fingerprint IS NOT NULL`, 1, before["Album/damaged.flac"])
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 3, root.ID)
	requireDiagnosticCount(t, fixture, scan.ID, "unsupported_format", 1)
	requireDiagnosticCount(t, fixture, scan.ID, "parse_failure", 1)
	if scan.Diagnostics.Total != 2 {
		t.Errorf("过滤项不应产生诊断：%+v", scan.Diagnostics)
	}
	if scan.Progress == nil || scan.Progress.FilesCounted != 3 || scan.Progress.FilesProcessed != 3 {
		t.Errorf("预计数与遍历必须同时过滤隐藏文件：%+v", scan.Progress)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_held_sources WHERE scan_run_id=$1::uuid`, 0, scan.ID)
}

func filterCueDocument(reference string) []byte {
	return []byte(strings.Join([]string{
		`TITLE "Cue Album"`, `PERFORMER "Artist"`, `FILE "` + reference + `" FLAC`,
		`  TRACK 01 AUDIO`, `    TITLE "First"`, `    INDEX 01 00:00:00`,
		`  TRACK 02 AUDIO`, `    TITLE "Second"`, `    INDEX 01 00:01:00`,
	}, "\n"))
}

func TestPostgreSQLScanLifecycleHeldUnreadableDirectoryOnlyProtectsItsSubtree(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("权限用例必须由非 root 用户运行")
	}
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "held-directory")
	service := scannerTestApplication(fixture)
	for index, relative := range []string{"Blocked/01.flac", "Blocked/Cue/image.flac", "Other/deleted.flac", "Other/live.flac"} {
		writeAudio(t, root, relative, audioFile{seed: byte(index + 1), title: relative, album: "Album", track: index + 1})
	}
	writeFixtureFile(t, root, "Blocked/Cue/album.cue", filterCueDocument("image.flac"))
	requireFilterScan(t, runFilterLifecycleScan(t, service), "succeeded", 0)
	before := presentTrackIDs(t, fixture, root.ID)
	if len(before) != 5 {
		t.Fatalf("首次扫描应含物理轨与 CUE 虚拟轨：%v", before)
	}
	blocked := filepath.Join(root.Path, "Blocked")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(blocked, 0o755); err != nil {
			t.Errorf("恢复合成目录权限：%v", err)
		}
	})
	if _, err := os.ReadDir(blocked); !os.IsPermission(err) {
		t.Fatalf("chmod 000 未造成真实权限拒绝：%v", err)
	}
	removeFilterFixture(t, root, "Other/deleted.flac")

	scan := runFilterLifecycleScan(t, service)
	requireFilterScan(t, scan, "succeeded", 1)
	after := presentTrackIDs(t, fixture, root.ID)
	for relative, id := range before {
		if strings.HasPrefix(relative, "Blocked/") || relative == "Other/live.flac" {
			requireTrackAt(t, after, relative, id)
		}
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE id=$1::uuid AND source_status='missing'`, 1, before["Other/deleted.flac"])
	requireDiagnosticCount(t, fixture, scan.ID, "walk_error", 1)
	if scan.Progress == nil || scan.Progress.CountComplete {
		t.Errorf("读不了目录时预计数应不完整：%+v", scan.Progress)
	}
}

func TestPostgreSQLScanLifecycleHeldUnsafeCueKeepsVirtualTracks(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "held-cue")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Cue/image.flac", audioFile{seed: 1, title: "Image", album: "Cue Album", track: 1})
	writeFixtureFile(t, root, "Cue/album.cue", filterCueDocument("image.flac"))
	writeAudio(t, root, "Other/deleted.flac", audioFile{seed: 2, title: "Deleted", album: "Other", track: 1})
	requireFilterScan(t, runFilterLifecycleScan(t, service), "succeeded", 0)
	before := presentTrackIDs(t, fixture, root.ID)
	if len(before) != 3 {
		t.Fatalf("首次扫描应含两条虚拟轨和待删除曲目：%v", before)
	}
	writeFixtureFile(t, root, "Cue/album.cue", filterCueDocument("../../../outside.flac"))
	removeFilterFixture(t, root, "Other/deleted.flac")

	scan := runFilterLifecycleScan(t, service)
	requireFilterScan(t, scan, "succeeded", 1)
	after := presentTrackIDs(t, fixture, root.ID)
	for relative, id := range before {
		if strings.HasPrefix(relative, "Cue/album.cue#") {
			requireTrackAt(t, after, relative, id)
			assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE id=$1::uuid AND source_kind='cue_virtual' AND cue_sheet_path='Cue/album.cue'`, 1, id)
		}
	}
	requireDiagnosticCount(t, fixture, scan.ID, "cue_reference", 1)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE id=$1::uuid AND source_status='missing'`, 1, before["Other/deleted.flac"])
}

func TestPostgreSQLHeldTrackCannotLoseUUIDToNewMatchingFingerprint(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "held-identity")
	service := scannerTestApplication(fixture)
	original := audioFile{seed: 7, title: "Original", album: "Album", track: 1}
	writeAudio(t, root, "Album/original.flac", original)
	requireFilterScan(t, runFilterLifecycleScan(t, service), "succeeded", 0)
	before := readTracks(t, fixture, root.ID)
	if len(before) != 1 || before[0].Fingerprint == "" {
		t.Fatalf("首次扫描必须保存可配对指纹：%+v", before)
	}
	writeFixtureFile(t, root, "Album/original.flac", []byte("damaged"))
	writeAudio(t, root, "New/copy.flac", original)

	scan := runFilterLifecycleScan(t, service)
	requireFilterScan(t, scan, "succeeded", 1)
	after := presentTrackIDs(t, fixture, root.ID)
	requireTrackAt(t, after, "Album/original.flac", before[0].ID)
	if len(after) != 2 || after["New/copy.flac"] == "" || after["New/copy.flac"] == before[0].ID {
		t.Fatalf("新文件应得到独立 UUID，不能抢走保住的曲目：%v", after)
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_status='present' AND content_fingerprint=$2`, 2, root.ID, before[0].Fingerprint)
}

func TestPostgreSQLEmptyRootProtection(t *testing.T) {
	for _, scenario := range []string{"previously_populated", "new_root", "only_subdirectory_excluded"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			root := insertScannerRoot(t, fixture, "empty-root")
			service := scannerTestApplication(fixture)
			var oldID string
			if scenario != "new_root" {
				writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "Track", album: "Album", track: 1})
				requireFilterScan(t, runFilterLifecycleScan(t, service), "succeeded", 0)
				oldID = presentTrackIDs(t, fixture, root.ID)["Album/01.flac"]
				if scenario == "previously_populated" {
					if err := os.RemoveAll(filepath.Join(root.Path, "Album")); err != nil {
						t.Fatal(err)
					}
				} else if _, err := fixture.database.Exec(`INSERT INTO library_root_exclusions(root_id,relative_path) VALUES($1::uuid,'Album')`, root.ID); err != nil {
					t.Fatal(err)
				}
			}

			scan := runFilterLifecycleScan(t, service)
			if scenario == "previously_populated" {
				requireFilterScan(t, scan, "incomplete", 0)
				requireDiagnosticCount(t, fixture, scan.ID, "root_empty", 1)
				requireTrackAt(t, presentTrackIDs(t, fixture, root.ID), "Album/01.flac", oldID)
			} else {
				requireFilterScan(t, scan, "succeeded", 0)
				requireDiagnosticCount(t, fixture, scan.ID, "root_empty", 0)
				if scenario == "only_subdirectory_excluded" {
					assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE id=$1::uuid AND source_status='missing'`, 1, oldID)
				} else {
					assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid`, 0, root.ID)
				}
			}
		})
	}
}

func TestPostgreSQLArtworkIntegrationUnreadableCoverKeepsExistingBinding(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "bad-cover")
	service := scannerTestApplication(fixture)
	writeAudio(t, root, "Album/01.flac", audioFile{seed: 1, title: "Track", album: "Album", track: 1})
	writeFixtureFile(t, root, "Album/cover.jpg", jpegArtworkOfWidth(2))
	requireFilterScan(t, runFilterLifecycleScan(t, service), "succeeded", 0)
	var releaseID, originalKey string
	if err := fixture.database.QueryRow(`SELECT r.id::text,a.storage_key FROM releases r JOIN release_artworks a ON a.release_id=r.id WHERE r.source_root_id=$1::uuid`, root.ID).Scan(&releaseID, &originalKey); err != nil {
		t.Fatalf("读取原封面关系：%v", err)
	}
	original, err := os.ReadFile(filepath.Join(service.dataDirectory, "artwork", originalKey))
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, "Album/cover.jpg", []byte("not an image"))

	scan := runFilterLifecycleScan(t, service)
	requireFilterScan(t, scan, "succeeded", 0)
	requireDiagnosticCount(t, fixture, scan.ID, "artwork_unreadable", 1)
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM release_artworks WHERE release_id=$1::uuid AND storage_key=$2 AND mime_type='image/jpeg' AND width=2`, 1, releaseID, originalKey)
	stored, err := os.ReadFile(filepath.Join(service.dataDirectory, "artwork", originalKey))
	if err != nil || string(stored) != string(original) {
		t.Fatalf("原受管封面被改动：err=%v", err)
	}
}
