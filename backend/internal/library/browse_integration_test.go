package library

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

func TestPostgreSQLLibraryBrowsePathBoundaries(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "browse-paths")
	service := scannerTestApplication(f)
	ctx := t.Context()
	for _, name := range []string{
		"Albums/visible.txt", "Albums/.hidden.txt", "Albums/@eaDir/hidden.txt",
		"Albums/$RECYCLE.BIN/hidden.txt", "Albums/private/nested/hidden.txt",
	} {
		writeFixtureFile(t, root, name, []byte("合成内容"))
	}
	directoryExec(t, f, `INSERT INTO library_root_exclusions(root_id,relative_path) VALUES($1,'Albums/private')`, root.ID)
	outside := filepath.Join(f.allowedRoot, "outside")
	writeFixtureFile(t, registeredRoot{Path: outside}, "nested/escape.txt", []byte("根外合成内容"))
	for _, link := range []struct{ name, target string }{
		{"file-link.txt", filepath.Join(outside, "nested", "escape.txt")},
		{"dir-link", outside},
		{"inside-link", "Albums"},
		{"broken-link.txt", "absent.txt"},
	} {
		if err := os.Symlink(link.target, filepath.Join(root.Path, link.name)); err != nil {
			t.Fatal(err)
		}
	}

	checkPaths := func(t *testing.T, relative string, want error) {
		t.Helper()
		items, total, err := service.ListLibraryDirectory(ctx, root.ID, relative, 1, 20)
		if !errors.Is(err, want) || len(items) != 0 || total != 0 {
			t.Errorf("列出 %q = %+v / %d / %v，期望空结果与 %v", relative, items, total, err, want)
		}
		file, err := service.ReadLibraryFile(ctx, root.ID, relative)
		if !errors.Is(err, want) || !reflect.DeepEqual(file, LibraryFileRead{}) {
			t.Errorf("读取 %q = %+v / %v，期望空结果与 %v", relative, file, err, want)
		}
	}
	for _, tc := range []struct{ name, relative string }{
		{"parent", "../outside/nested/escape.txt"},
		{"within_path_parent", "Albums/../Albums/visible.txt"},
		{"absolute", filepath.Join(root.Path, "Albums", "visible.txt")},
		{"nul", "Albums/visible.txt\x00"},
		{"too_long", strings.Repeat("x/", 2048) + "x"},
	} {
		t.Run(tc.name, func(t *testing.T) { checkPaths(t, tc.relative, ErrInvalidLibraryPath) })
	}
	for _, relative := range []string{
		"file-link.txt", "dir-link", "dir-link/nested", "dir-link/nested/escape.txt",
		"inside-link", "inside-link/visible.txt", "broken-link.txt",
		"Albums/.hidden.txt", "Albums/@eaDir", "Albums/@eaDir/hidden.txt",
		"Albums/$RECYCLE.BIN", "Albums/$RECYCLE.BIN/hidden.txt",
		"Albums/private", "Albums/private/nested", "Albums/private/nested/hidden.txt",
	} {
		t.Run(relative, func(t *testing.T) { checkPaths(t, relative, ErrLibraryPathNotFound) })
	}

	items, total, err := service.ListLibraryDirectory(ctx, root.ID, "", 1, 20)
	wantRoot := []struct{ name, kind string }{
		{"Albums", "directory"}, {"broken-link.txt", "link"}, {"dir-link", "link"},
		{"file-link.txt", "link"}, {"inside-link", "link"},
	}
	if err != nil || total != len(wantRoot) || len(items) != len(wantRoot) {
		t.Fatalf("根目录结果 = %+v / %d / %v，期望 %v", items, total, err, wantRoot)
	}
	for i, want := range wantRoot {
		item := items[i]
		if item.Name != want.name || item.Type != want.kind ||
			(want.kind == "link" && (item.SizeBytes != nil || len(item.Owners) != 0)) {
			t.Errorf("根目录项 %+v，期望 %v", item, want)
		}
	}
	items, total, err = service.ListLibraryDirectory(ctx, root.ID, "Albums", 1, 1)
	if err != nil || total != 1 || len(items) != 1 || items[0].Name != "visible.txt" {
		t.Fatalf("Albums 过滤与分页结果 = %+v / %d / %v，期望只有 visible.txt", items, total, err)
	}
	file, err := service.ReadLibraryFile(ctx, root.ID, "Albums/visible.txt")
	if err != nil || file.Text == nil || *file.Text != "合成内容" {
		t.Fatalf("读取根内普通文件 = %+v / %v", file, err)
	}

	directoryExec(t, f, `UPDATE library_roots SET status='disabled' WHERE id=$1`, root.ID)
	if _, _, err := service.ListLibraryDirectory(ctx, root.ID, "Albums", 1, 20); !errors.Is(err, ErrLibraryPathNotFound) {
		t.Errorf("列出停用根中的目录 = %v，期望 not_found", err)
	}
	if _, err := service.ReadLibraryFile(ctx, root.ID, "Albums/visible.txt"); !errors.Is(err, ErrLibraryPathNotFound) {
		t.Errorf("读取停用根中的文件 = %v，期望 not_found", err)
	}
}

func TestPostgreSQLLibraryBrowseDirectoryOwners(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "browse-owners")
	service := scannerTestApplication(f)
	const (
		ordinary = "普通发行"
		cue      = "分轨发行"
		missing  = "Ordinary/02-missing.flac"
	)
	writeLyricsFLAC(t, root, "Ordinary/01.flac", 1, 3, "ALBUM="+ordinary, "DISCNUMBER=2")
	writeLyricsFLAC(t, root, missing, 2, 4, "ALBUM="+ordinary, "DISCNUMBER=2")
	writeFixtureFile(t, root, "Ordinary/cover.png", pngArtworkOfWidth(5))
	writeFixtureFile(t, root, "Ordinary/unrelated.bin", []byte("合成其他文件"))
	writeAudio(t, root, "Audio/image.flac", audioFile{seed: 3, title: "Image", album: cue, track: 1})
	writeFixtureFile(t, root, "Sheets/album.cue", []byte(`TITLE "分轨发行"
PERFORMER "合成艺人"
FILE "../Audio/image.flac" FLAC
  TRACK 01 AUDIO
    INDEX 01 00:00:00
  TRACK 02 AUDIO
    INDEX 01 00:01:00
`))
	writeFixtureFile(t, root, "Sheets/booklet.pdf", []byte("%PDF-1.7"))
	requireScanStatus(t, runTestScan(t, f, service, root), "succeeded")
	ids := releaseIDsByTitle(t, f, root.ID)
	if ids[ordinary] == "" || ids[cue] == "" || ids[ordinary] == ids[cue] {
		t.Fatalf("扫描应登记两张不同的合成发行，实际为 %v", ids)
	}
	// 模拟扫描状态为缺失后文件又重新出现，目录可见但归属须等下次扫描。
	directoryExec(t, f, `UPDATE tracks SET source_status='missing' WHERE source_root_id=$1 AND relative_path=$2`, root.ID, missing)
	assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE source_root_id=$1 AND relative_path=$2 AND source_status='missing'`, 1, root.ID, missing)

	owner := func(title string, disc, track int) catalog.LibraryFileOwner {
		return catalog.LibraryFileOwner{ReleaseID: ids[title], ReleaseTitle: title, DiscNumber: disc, TrackNumber: track}
	}
	type expectedEntry struct {
		name, kind string
		owners     []catalog.LibraryFileOwner
	}
	for _, tc := range []struct {
		name, directory   string
		page, size, total int
		items             []expectedEntry
	}{
		{"ordinary_first_page", "Ordinary", 1, 2, 4, []expectedEntry{
			{"01.flac", "audio", []catalog.LibraryFileOwner{owner(ordinary, 2, 3)}},
			{"02-missing.flac", "audio", nil},
		}},
		{"ordinary_second_page", "Ordinary", 2, 2, 4, []expectedEntry{
			{"cover.png", "image", []catalog.LibraryFileOwner{owner(ordinary, 0, 0)}},
			{"unrelated.bin", "other", nil},
		}},
		{"past_last_page", "Ordinary", 3, 2, 4, nil},
		{"cue_sheet_and_attachment", "Sheets", 1, 20, 2, []expectedEntry{
			{"album.cue", "text", []catalog.LibraryFileOwner{owner(cue, 1, 1), owner(cue, 1, 2)}},
			{"booklet.pdf", "other", []catalog.LibraryFileOwner{owner(cue, 0, 0)}},
		}},
		{"cue_parent", "Audio", 1, 20, 1, []expectedEntry{
			{"image.flac", "audio", []catalog.LibraryFileOwner{owner(cue, 1, 1), owner(cue, 1, 2)}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, total, err := service.ListLibraryDirectory(t.Context(), root.ID, tc.directory, tc.page, tc.size)
			if err != nil || total != tc.total || len(items) != len(tc.items) {
				t.Fatalf("目录页结果 = %+v / %d / %v，期望 %d 项、总共 %d 项", items, total, err, len(tc.items), tc.total)
			}
			for i, want := range tc.items {
				item := items[i]
				if item.Name != want.name || item.Type != want.kind || !slices.Equal(item.Owners, want.owners) {
					t.Errorf("目录项 %d = %+v，期望 %s / %s / %+v", i, item, want.name, want.kind, want.owners)
				}
			}
			raw, err := json.Marshal(items)
			if err != nil || bytes.Contains(raw, []byte(root.Path)) {
				t.Fatalf("目录 JSON 泄露登记根绝对路径或无法编码：%s / %v", raw, err)
			}
		})
	}
}
