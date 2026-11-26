package library

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func writeScanReadMP3(t *testing.T, root registeredRoot, relative string, seed, track byte, title, artist, album string, v1, unicodeTitle bool, catalogNumber string) {
	t.Helper()
	frame := bytes.Repeat([]byte{seed}, 417)
	copy(frame, []byte{0xff, 0xfb, 0x90, 0x00})
	audio := bytes.Repeat(frame, 8)
	if v1 {
		tag := make([]byte, 128)
		copy(tag, "TAG")
		copy(tag[3:33], title)
		copy(tag[33:63], artist)
		copy(tag[63:93], album)
		copy(tag[93:97], "1999")
		tag[126], tag[127] = track, 17
		writeFixtureFile(t, root, relative, append(audio, tag...))
		return
	}
	encoding := byte(0)
	if unicodeTitle {
		encoding = 3
	}
	var tags []byte
	appendTag := func(key string, enc byte, value string) {
		var header [10]byte
		copy(header[:4], key)
		binary.BigEndian.PutUint32(header[4:8], uint32(1+len(value)))
		tags = append(tags, header[:]...)
		tags = append(append(tags, enc), value...)
	}
	appendTag("TIT2", encoding, title)
	appendTag("TPE1", 0, artist)
	appendTag("TALB", 0, album)
	appendTag("TRCK", 0, fmt.Sprint(track))
	if catalogNumber != "" {
		appendTag("TXXX", 0, "CATALOGNUMBER\x00"+catalogNumber)
	}
	n := len(tags)
	header := []byte{'I', 'D', '3', 3, 0, 0, byte(n>>21) & 127, byte(n>>14) & 127, byte(n>>7) & 127, byte(n) & 127}
	writeFixtureFile(t, root, relative, bytes.Join([][]byte{header, tags, audio}, nil))
}

type scanReadLegacyTrack struct {
	title, titleSource, titleBytes, artist, release string
	tags                                            *metadata.ScanTags
}

func scanReadLegacyEvidence(t *testing.T, f *libraryFixture, root registeredRoot, relative string) scanReadLegacyTrack {
	t.Helper()
	var result scanReadLegacyTrack
	var id string
	err := f.database.QueryRowContext(t.Context(), `SELECT t.id,t.title,t.artist,m.release_id,
		COALESCE((SELECT source_kind FROM track_observations WHERE track_id=t.id AND field_name='title'),''),
		COALESCE((SELECT value FROM track_observations WHERE track_id=t.id AND field_name='legacy_bytes_title'),'')
		FROM tracks t JOIN media m ON m.id=t.medium_id
		WHERE t.source_root_id=$1 AND t.relative_path=$2 AND t.source_status='present'`, root.ID, relative).
		Scan(&id, &result.title, &result.artist, &result.release, &result.titleSource, &result.titleBytes)
	if err != nil {
		t.Fatalf("读取 %s 的扫描值与编码证据：%v", relative, err)
	}
	result.tags = scanReadTagJSON(t, f, `SELECT scan_tags FROM tracks WHERE id=$1`, id)
	return result
}

func TestPostgreSQLScanReadAlbumEncoding(t *testing.T) {
	const ambiguous, gbkOnly, big5Only = "\xcd\xf5\xb7\xc6", "\x81\x80", "\xa4\xf4"
	t.Run("cached_album_members_are_decoded_together", func(t *testing.T) {
		f := newLibraryFixture(t)
		root := insertScannerRoot(t, f, "encoding-recompute")
		service, logs := newIncrementalTestService(t, f)
		const first, second = "Album/01.mp3", "Album/02.mp3"
		writeScanReadMP3(t, root, first, 21, 1, ambiguous, "Artist", "Album", true, false, "")
		writeScanReadMP3(t, root, second, 22, 2, "普通标题", "Artist", "Album", false, true, "")
		runIncrementalTestScan(t, service, logs, root)
		before := scanReadLegacyEvidence(t, f, root, first)
		if before.title != "Íõ·Æ" || before.titleSource != "garbled" || before.titleBytes != base64.StdEncoding.EncodeToString([]byte(ambiguous)) ||
			before.tags == nil || before.tags.LegacyEncodings == nil || !slices.Contains(*before.tags.LegacyEncodings, "GBK") ||
			!slices.Contains(*before.tags.LegacyEncodings, "Big5") {
			t.Errorf("单疑似字段多解时应保留原样、原字节及编码列表：%+v，快照 %+v", before, before.tags)
		}
		if got := scanReadLegacyEvidence(t, f, root, second); got.title != "普通标题" || got.titleSource != "mp3_id3v2" || got.titleBytes != "" {
			t.Errorf("UTF-8 标题误作旧编码：%+v", got)
		}
		writeScanReadMP3(t, root, second, 22, 2, gbkOnly, "Artist", "Album", false, false, "")
		_, stats := runIncrementalTestScan(t, service, logs, root)
		if stats.ParseCacheHits != 1 || stats.FilesParsed != 1 {
			t.Errorf("修改一轨后应复用另一轨的原始缓存：%+v", stats)
		}
		for path, title := range map[string]string{first: "王菲", second: "亐"} {
			got := scanReadLegacyEvidence(t, f, root, path)
			if got.title != title || got.titleSource != "legacy_encoding" || got.release != before.release {
				t.Errorf("%s 用候选唯一编码解得 %+v，期望原发行中的 %q/legacy_encoding", path, got, title)
			}
		}
		if raw := readIncrementalTestCache(t, f, root)[first].Observation; raw.Title != "Íõ·Æ" || !bytes.Equal(raw.LegacyText["title"], []byte(ambiguous)) {
			t.Errorf("已解码的扫描候选覆盖了原始文件缓存：%q/%x", raw.Title, raw.LegacyText["title"])
		}
		requireNoScanLegacyWrites(t, f)
	})

	t.Run("ambiguous_empty_intersection_western_and_same_folder_candidates", func(t *testing.T) {
		f := newLibraryFixture(t)
		root := insertScannerRoot(t, f, "encoding-albums")
		service, logs := newIncrementalTestService(t, f)
		for _, input := range []struct {
			path, title, album string
			track              byte
		}{
			{"Multiple/01.mp3", ambiguous, "Multiple", 1},
			{"Multiple/02.mp3", ambiguous, "Multiple", 2},
			{"Conflict/01.mp3", gbkOnly, "Conflict", 1},
			{"Conflict/02.mp3", big5Only, "Conflict", 2},
			{"Western/01.mp3", "Caf\xe9", "Western", 1},
			{"Mixed/01.mp3", ambiguous, "Album A", 1},
			{"Mixed/02.mp3", ambiguous, "Album A", 2},
			{"Mixed/07.mp3", gbkOnly, "Album B", 7},
		} {
			writeScanReadMP3(t, root, input.path, byte(len(input.path))+input.track, input.track, input.title, "Artist", input.album, false, false, "")
		}
		runIncrementalTestScan(t, service, logs, root)
		for _, path := range []string{"Multiple/01.mp3", "Multiple/02.mp3", "Mixed/01.mp3", "Mixed/02.mp3"} {
			got := scanReadLegacyEvidence(t, f, root, path)
			if got.title != "Íõ·Æ" || got.titleSource != "garbled" || got.titleBytes != "zfW3xg==" ||
				got.tags == nil || got.tags.LegacyEncodings == nil || len(*got.tags.LegacyEncodings) < 2 {
				t.Errorf("%s 的多解证据 = %+v，快照 %+v", path, got, got.tags)
			}
		}
		for _, input := range []struct{ path, raw string }{{"Conflict/01.mp3", gbkOnly}, {"Conflict/02.mp3", big5Only}} {
			got := scanReadLegacyEvidence(t, f, root, input.path)
			if got.titleSource != "garbled" || got.titleBytes != base64.StdEncoding.EncodeToString([]byte(input.raw)) ||
				got.tags == nil || got.tags.LegacyEncodings == nil || len(*got.tags.LegacyEncodings) != 0 {
				t.Errorf("%s 的无交集证据 = %+v，快照 %+v", input.path, got, got.tags)
			}
		}
		mixedA := scanReadLegacyEvidence(t, f, root, "Mixed/01.mp3")
		mixedB := scanReadLegacyEvidence(t, f, root, "Mixed/07.mp3")
		if mixedA.release == mixedB.release || mixedB.title != "亐" || mixedB.titleSource != "legacy_encoding" {
			t.Errorf("同目录两个候选互相影响：A %+v，B %+v", mixedA, mixedB)
		}
		western := scanReadLegacyEvidence(t, f, root, "Western/01.mp3")
		if western.title != "Café" || western.titleSource != "mp3_id3v2" || western.titleBytes != "" {
			t.Errorf("正常西文重音被作为旧编码：%+v", western)
		}
		requireNoScanLegacyWrites(t, f)
	})

	t.Run("cue_overrides_parent_legacy_fields", func(t *testing.T) {
		f := newLibraryFixture(t)
		root := insertScannerRoot(t, f, "encoding-cue")
		service, logs := newIncrementalTestService(t, f)
		writeScanReadMP3(t, root, "Cue/audio.mp3", 18, 1, ambiguous, gbkOnly, ambiguous, false, false, "")
		doc := bytes.Replace(filterCueDocument("audio.mp3"), []byte("00:01:00"), []byte("00:00:05"), 1)
		writeFixtureFile(t, root, "Cue/00.cue", doc)
		runIncrementalTestScan(t, service, logs, root)
		assertDatabaseCount(t, f, `SELECT count(*) FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id
			WHERE t.source_root_id=$1 AND t.source_kind='cue_virtual' AND t.source_status='present'
			AND t.title IN ('First','Second') AND t.artist='Artist' AND r.title='Cue Album'
			AND NOT COALESCE(t.scan_tags ? 'legacy_encodings',false)
			AND NOT EXISTS (SELECT FROM track_observations o WHERE o.track_id=t.id AND o.source_kind IN ('garbled','legacy_encoding'))`, 2, root.ID)
		requireNoScanLegacyWrites(t, f)
	})

	t.Run("search_uses_identifier_without_garbled_text", func(t *testing.T) {
		f := newLibraryFixture(t)
		root := insertScannerRoot(t, f, "encoding-search")
		service, logs := newIncrementalTestService(t, f)
		const relative, number = "Mix/01.mp3", "SCN-017"
		writeScanReadMP3(t, root, relative, 22, 1, ambiguous, ambiguous, ambiguous, false, false, number)
		runIncrementalTestScan(t, service, logs, root)
		track := scanReadLegacyEvidence(t, f, root, relative)
		entries, err := catalog.LoadCurationEvidence(t.Context(), f.database, []string{track.release})
		if err != nil {
			t.Fatal(err)
		}
		entry := entries[track.release]
		if len(entry.Tracks) != 1 || entry.Tracks[0].Authenticity["title"].SearchValue(track.title) != "" ||
			entry.Tracks[0].Authenticity["artist"].SearchValue(track.artist) != "" {
			t.Errorf("未决乱码进入可搜索的曲目证据：%+v", entry.Tracks)
		}
		finder := &scanReadReleaseFinder{}
		if _, err := program.Match(t.Context(), entry, finder, nil); err != nil {
			t.Fatal(err)
		}
		foundNumber := false
		for _, q := range finder.queries {
			foundNumber = foundNumber || q.CatalogNumber == number
			if q.Title != "" || q.Artist != "" {
				t.Errorf("未决乱码进入整理搜索：%+v", q)
			}
		}
		if !foundNumber {
			t.Errorf("独立编号 %q 应继续参与检索，实际查询 %+v", number, finder.queries)
		}
		requireNoScanLegacyWrites(t, f)
	})
}

func requireNoScanLegacyWrites(t *testing.T, f *libraryFixture) {
	t.Helper()
	assertDatabaseCount(t, f, `SELECT count(*) FROM curation_settings WHERE enabled`, 0)
	assertDatabaseCount(t, f, `SELECT count(*) FROM operations WHERE operation_type LIKE '%legacy_encoding%' OR after_state::text LIKE '%legacy_encoding%'`, 0)
}

type scanReadReleaseFinder struct {
	queries []curation.ReleaseSearch
}

func (f *scanReadReleaseFinder) SearchReleasePage(_ context.Context, q curation.ReleaseSearch) (curation.ReleasePage, error) {
	f.queries = append(f.queries, q)
	return curation.ReleasePage{PageInfo: curation.PageInfo{Complete: true}}, nil
}

func (*scanReadReleaseFinder) ReleaseByID(context.Context, string) (sources.Release, error) {
	return sources.Release{}, fmt.Errorf("夹具没有发行对应")
}

func (*scanReadReleaseFinder) ReleasesByBarcode(context.Context, string) ([]sources.Release, error) {
	return nil, fmt.Errorf("夹具没有条码")
}

func (*scanReadReleaseFinder) ReleasesByCatalog(context.Context, string, string) ([]sources.Release, error) {
	return nil, fmt.Errorf("夹具使用分页编号搜索")
}

func (*scanReadReleaseFinder) ReleasesByDisc(context.Context, sources.DiscTOC) (sources.DiscMatch, error) {
	return sources.DiscMatch{}, fmt.Errorf("夹具不产生 TOC 请求")
}

func (f *scanReadReleaseFinder) SearchReleases(_ context.Context, q sources.ReleaseQuery) ([]sources.Release, error) {
	f.queries = append(f.queries, curation.ReleaseSearch{Title: strings.TrimSpace(q.Title), Artist: strings.TrimSpace(q.Artist), TrackCount: q.TrackCount})
	return nil, nil
}
