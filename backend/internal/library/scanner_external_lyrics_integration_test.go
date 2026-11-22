package library

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"golang.org/x/text/encoding/simplifiedchinese"
)

func requireCatalogLyricsSource(t *testing.T, fixture *libraryFixture, id, source string) {
	t.Helper()
	response, err := catalog.NewStore(fixture.database).LoadTrackLyrics(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if response.TrackID != id {
		t.Fatalf("返回曲目=%q，期望=%q", response.TrackID, id)
	}
	if source == "" {
		if response.Lyrics != nil {
			t.Fatalf("应当没有歌词：%+v", response.Lyrics)
		}
		return
	}
	if response.Lyrics == nil || response.Lyrics.Source != source {
		t.Fatalf("歌词来源应为 %q，实际=%+v", source, response.Lyrics)
	}
}

func TestPostgreSQLExternalLyricsEncodingAndFallback(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "external-encoding")
	service := scannerTestApplication(fixture)
	const text = "[00:01.00]你好𠀀\n[00:02.00]外置歌词"
	utf16Data := func(order binary.AppendByteOrder, bom []byte) []byte {
		out := append([]byte{}, bom...)
		for _, unit := range utf16.Encode([]rune(text)) {
			out = order.AppendUint16(out, unit)
		}
		return out
	}
	gb, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		data     []byte
		external bool
		link     bool
	}{
		{"UTF-8", []byte(text), true, false},
		{"UTF-8 BOM", append([]byte{0xef, 0xbb, 0xbf}, []byte(text)...), true, false},
		{"UTF-16LE", utf16Data(binary.LittleEndian, []byte{0xff, 0xfe}), true, false},
		{"UTF-16BE", utf16Data(binary.BigEndian, []byte{0xfe, 0xff}), true, false},
		{"GB18030", gb, true, false},
		{"空文件", nil, false, false},
		{"非法编码", []byte{0xff, 0xfe, 0, 0xd8}, false, false},
		{"替换字符", []byte{0x81}, false, false},
		{"UTF-32", []byte{0xff, 0xfe, 0, 0, 'a', 0, 0, 0}, false, false},
		{"原始超限", []byte(strings.Repeat("\x00", parser.MaxExternalLyricsBytes) + "a"), false, false},
		{"规范化后超限", []byte(strings.Repeat("a", metadata.MaxLyricsBytes+1)), false, false},
		{"符号链接", []byte(text), false, true},
	}
	for i, tc := range cases {
		path := fmt.Sprintf("Album/%02d", i+1)
		writeLyricsFLAC(t, root, path+".flac", byte(i+1), i+1, "LYRICS=[00:01.00]内嵌歌词")
		if tc.link {
			writeFixtureFile(t, root, path+".txt", tc.data)
			if err := os.Symlink(filepath.Base(path)+".txt", filepath.Join(root.Path, path+".lrc")); err != nil {
				t.Fatal(err)
			}
		} else {
			writeFixtureFile(t, root, path+".lrc", tc.data)
		}
	}
	before := lyricsDirectorySnapshot(t, root.Path)
	scan := runTestScan(t, fixture, service, root)
	requireScanStatus(t, scan, "succeeded")
	requireLyricsDirectoryUnchanged(t, root.Path, before)
	stored, ids := readStoredLyrics(t, fixture, root.ID), presentTrackIDs(t, fixture, root.ID)
	if len(ids) != len(cases) {
		t.Fatalf("曲目数量=%d，期望=%d", len(ids), len(cases))
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := fmt.Sprintf("Album/%02d.flac", i+1)
			want, source, apiSource := "[00:01.00]内嵌歌词", parser.LyricsSourceVorbisLyrics, catalog.LyricsSourceEmbedded
			if tc.external {
				want, source, apiSource = text, parser.LyricsSourceLRCFile, catalog.LyricsSourceExternal
			}
			requireStoredLyrics(t, stored, path, want, source)
			requireCatalogLyricsSource(t, fixture, ids[path], apiSource)
			cached := readIncrementalTestCache(t, fixture, root)[path].Observation
			if cached.Lyrics != "[00:01.00]内嵌歌词" || cached.LyricsSource != parser.LyricsSourceVorbisLyrics {
				t.Fatalf("外置歌词污染解析缓存：%+v", cached)
			}
		})
	}
	assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM scan_diagnostics WHERE scan_run_id=$1::uuid`, 0, scan.ID)
	// 完整重扫也使用外置歌词，不能把缓存外覆盖限定在增量模式。
	before = lyricsDirectorySnapshot(t, root.Path)
	requireScanStatus(t, runBorrowFullScan(t, fixture, service, root), "succeeded")
	requireLyricsDirectoryUnchanged(t, root.Path, before)
	if after := readStoredLyrics(t, fixture, root.ID); !reflect.DeepEqual(stored, after) {
		t.Fatalf("完整重扫歌词结果变化：%v -> %v", stored, after)
	}
	if after := presentTrackIDs(t, fixture, root.ID); !reflect.DeepEqual(ids, after) {
		t.Fatalf("完整重扫曲目身份变化：%v -> %v", ids, after)
	}

}

func TestPostgreSQLExternalLyricsIncrementalAndCredits(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "external-incremental")
	service, logs := newIncrementalTestService(t, fixture)
	const embedded = "作词：内嵌作词\n作曲：内嵌作曲\n[00:01.00]内嵌正文"
	writeLyricsFLAC(t, root, "Album/01.flac", 1, 1, "LYRICS="+embedded, "COMPOSER=标签作曲")
	writeLyricsFLAC(t, root, "Album/02.flac", 2, 2)
	writeAudio(t, root, "Other/01.flac", audioFile{seed: 3, title: "Other", album: "Other", track: 1})
	before := lyricsDirectorySnapshot(t, root.Path)
	runIncrementalTestScan(t, service, logs, root)
	requireLyricsDirectoryUnchanged(t, root.Path, before)
	ids := presentTrackIDs(t, fixture, root.ID)
	cacheBefore := readIncrementalTestCache(t, fixture, root)
	// 只修改旁文件；另一发行应始终被跳过。
	for _, phase := range []struct {
		name, text string
		remove     bool
	}{
		{name: "新增", text: "作词：外置作词\n作曲：外置作曲\n[00:01.00]外置正文"},
		{name: "修改", text: "作词：更新作词\n作曲：外置作曲\n[00:02.00]更新正文"},
		{name: "删除后回落或清空", remove: true},
	} {
		t.Run(phase.name, func(t *testing.T) {
			for _, stem := range []string{"01", "02"} {
				path := "Album/" + stem + ".lrc"
				if phase.remove {
					if err := os.Remove(filepath.Join(root.Path, path)); err != nil {
						t.Fatal(err)
					}
				} else {
					writeFixtureFile(t, root, path, []byte(phase.text))
				}
			}
			before := lyricsDirectorySnapshot(t, root.Path)
			_, summary := runIncrementalTestScan(t, service, logs, root)
			requireLyricsDirectoryUnchanged(t, root.Path, before)
			if summary.FilesParsed != 0 || summary.ParseCacheHits != 3 || summary.ParseCacheWrites != 0 || summary.CandidatesWritten != 1 || summary.CandidatesSkipped != 1 {
				t.Fatalf("只改歌词的增量摘要错误：%+v", summary)
			}
			if after := presentTrackIDs(t, fixture, root.ID); !reflect.DeepEqual(ids, after) {
				t.Fatalf("曲目身份变化：%v -> %v", ids, after)
			}
			stored := readStoredLyrics(t, fixture, root.ID)
			for _, path := range []string{"Album/01.flac", "Album/02.flac"} {
				want, source, apiSource := phase.text, parser.LyricsSourceLRCFile, catalog.LyricsSourceExternal
				if phase.remove {
					want, source, apiSource = "", "", ""
					if path == "Album/01.flac" {
						want, source, apiSource = embedded, parser.LyricsSourceVorbisLyrics, catalog.LyricsSourceEmbedded
					}
				}
				requireStoredLyrics(t, stored, path, want, source)
				requireCatalogLyricsSource(t, fixture, ids[path], apiSource)
			}
			for path, entry := range readIncrementalTestCache(t, fixture, root) {
				if !reflect.DeepEqual(entry.Observation, cacheBefore[path].Observation) || !entry.UpdatedAt.Equal(cacheBefore[path].UpdatedAt) {
					t.Fatalf("旁文件改动污染缓存 %s", path)
				}
			}
			lyricist := "外置作词"
			if phase.name == "修改" {
				lyricist = "更新作词"
			}
			if phase.remove {
				lyricist = "内嵌作词"
			}
			assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM track_credits WHERE track_id=$1::uuid AND role='lyricist' AND name=$2 AND source='lyrics'`, 1, ids["Album/01.flac"], lyricist)
			assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM track_credits WHERE track_id=$1::uuid AND role='lyricist'`, 1, ids["Album/01.flac"])
			assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM track_credits WHERE track_id=$1::uuid AND role='composer' AND name='标签作曲' AND source='tag'`, 1, ids["Album/01.flac"])
			assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM track_credits WHERE track_id=$1::uuid AND role='composer'`, 1, ids["Album/01.flac"])
			if !phase.remove {
				assertDatabaseCount(t, fixture, `SELECT COUNT(*) FROM track_credits WHERE track_id=$1::uuid AND name IN ('内嵌作词','内嵌作曲')`, 0, ids["Album/01.flac"])
			}
		})
	}
}
