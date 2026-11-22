package library

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/library/parser"
)

const (
	scanMBID1 = "a1234567-0000-3000-8000-000000000001"
	scanMBID2 = "a1234567-0000-3000-8000-000000000002"
	scanMBID3 = "a1234567-0000-3000-8000-000000000003"
	scanDisc  = "AbCdEf0123456789._-abcdefgh-"
)

type scannedIdentifier struct {
	kind, value string
	ordinal     int
}

type scannedIdentifierRow struct {
	scannedIdentifier
	xmin string
}

func readScannedIdentifiers(t *testing.T, fixture *libraryFixture, trackID string) []scannedIdentifierRow {
	t.Helper()
	rows, err := fixture.database.Query(
		"SELECT kind,value,ordinal,xmin::text FROM track_identifiers WHERE track_id=$1::uuid ORDER BY kind,ordinal,value", trackID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []scannedIdentifierRow
	for rows.Next() {
		var row scannedIdentifierRow
		if err := rows.Scan(&row.kind, &row.value, &row.ordinal, &row.xmin); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func requireScannedIdentifiers(t *testing.T, fixture *libraryFixture, trackID string, want ...scannedIdentifier) []scannedIdentifierRow {
	t.Helper()
	rows := readScannedIdentifiers(t, fixture, trackID)
	var got []scannedIdentifier
	for _, row := range rows {
		got = append(got, row.scannedIdentifier)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("曲目 %s 标识行 = %+v，期望 %+v", trackID, got, want)
	}
	return rows
}

func TestPostgreSQLTrackIdentifiersReplaceAndClear(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "track-identifiers")
	service, logs := newIncrementalTestService(t, fixture)
	const path = "Album/01.flac"
	writeLyricsFLAC(t, root, path, 41, 1,
		"MUSICBRAINZ_ARTISTID="+strings.ToUpper(scanMBID2),
		"MUSICBRAINZ_ARTISTID="+scanMBID1,
		"MUSICBRAINZ_ARTISTID="+scanMBID2,
		"MUSICBRAINZ_TRACKID="+scanMBID3,
		"ISRC=us-x1z-25-00001",
		"ACOUSTID_FINGERPRINT="+scanMBID3)
	runIncrementalTestScan(t, service, logs, root)
	ids := presentTrackIDs(t, fixture, root.ID)
	track := ids[path]
	if len(ids) != 1 || track == "" {
		t.Fatalf("期望一个物理轨，实际 %+v", ids)
	}
	first := requireScannedIdentifiers(t, fixture, track,
		scannedIdentifier{"isrc", "USX1Z2500001", 0},
		scannedIdentifier{"mb_artist", scanMBID2, 0},
		scannedIdentifier{"mb_artist", scanMBID1, 1},
		scannedIdentifier{"mb_recording", scanMBID3, 0})

	states := readIncrementalTestCandidateStates(t, fixture, root)
	_, unchanged := runIncrementalTestScan(t, service, logs, root)
	if unchanged.CandidatesWritten != 0 || unchanged.CandidatesSkipped != 1 {
		t.Errorf("未变候选的扫描摘要 = %+v", unchanged)
	}
	if got := readScannedIdentifiers(t, fixture, track); !slices.Equal(got, first) {
		t.Errorf("未变候选的标识行应保留原事务版本，前 %+v，后 %+v", first, got)
	}
	if got := readIncrementalTestCandidateStates(t, fixture, root); !reflect.DeepEqual(got, states) {
		t.Errorf("未变候选的状态行发生变化，前 %+v，后 %+v", states, got)
	}

	writeLyricsFLAC(t, root, path, 41, 1, "MUSICBRAINZ_ARTISTID="+scanMBID3)
	runIncrementalTestScan(t, service, logs, root)
	requireTrackAt(t, presentTrackIDs(t, fixture, root.ID), path, track)
	requireScannedIdentifiers(t, fixture, track, scannedIdentifier{"mb_artist", scanMBID3, 0})

	writeLyricsFLAC(t, root, path, 41, 1)
	runIncrementalTestScan(t, service, logs, root)
	requireTrackAt(t, presentTrackIDs(t, fixture, root.ID), path, track)
	requireScannedIdentifiers(t, fixture, track)
}

func TestPostgreSQLTrackIdentifiersCueVirtual(t *testing.T) {
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "cue-identifiers")
	service, logs := newIncrementalTestService(t, fixture)
	writeLyricsFLAC(t, root, "Album/image.flac", 42, 1,
		"MUSICBRAINZ_ALBUMID="+scanMBID1,
		"MUSICBRAINZ_RELEASEGROUPID="+scanMBID2,
		"MUSICBRAINZ_ALBUMARTISTID="+scanMBID3,
		"MUSICBRAINZ_DISCID="+scanDisc,
		"MUSICBRAINZ_TRACKID="+scanMBID1,
		"MUSICBRAINZ_RELEASETRACKID="+scanMBID2,
		"MUSICBRAINZ_ARTISTID="+scanMBID3,
		"MUSICBRAINZ_WORKID="+scanMBID1,
		"ACOUSTID_ID="+scanMBID2,
		"ISRC=USX1Z2500001")
	writeFixtureFile(t, root, "Album/album.cue", []byte(strings.Join([]string{
		"TITLE \"Cue Release\"",
		"PERFORMER \"Cue Artist\"",
		"FILE \"image.flac\" WAVE",
		"  TRACK 01 AUDIO",
		"    TITLE \"First\"",
		"    ISRC GBA1B2500003",
		"    INDEX 01 00:00:00",
		"  TRACK 02 AUDIO",
		"    TITLE \"Second\"",
		"    INDEX 01 00:01:00",
	}, "\n")))

	runIncrementalTestScan(t, service, logs, root)
	assertDatabaseCount(t, fixture, "SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_status='present'", 2, root.ID)
	assertDatabaseCount(t, fixture, "SELECT COUNT(*) FROM tracks WHERE source_root_id=$1::uuid AND source_kind='cue_virtual' AND cue_isrc='GBA1B2500003'", 1, root.ID)
	for _, title := range []string{"First", "Second"} {
		var trackID string
		if err := fixture.database.QueryRow("SELECT id::text FROM tracks WHERE source_root_id=$1::uuid AND source_kind='cue_virtual' AND title=$2", root.ID, title).Scan(&trackID); err != nil {
			t.Fatalf("读取虚拟轨 %q 失败：%v", title, err)
		}
		requireScannedIdentifiers(t, fixture, trackID,
			scannedIdentifier{"mb_album_artist", scanMBID3, 0},
			scannedIdentifier{"mb_disc", scanDisc, 0},
			scannedIdentifier{"mb_release", scanMBID1, 0},
			scannedIdentifier{"mb_release_group", scanMBID2, 0})
	}
}

func TestPostgreSQLTrackIdentifiersReleaseFields(t *testing.T) {
	for _, format := range []string{"mp3", "m4a"} {
		t.Run(format, func(t *testing.T) {
			fixture := newLibraryFixture(t)
			root := insertScannerRoot(t, fixture, "release-tags")
			service, logs := newIncrementalTestService(t, fixture)
			path := "Album/01." + format
			tags := [][2]string{
				{"BARCODE", "012345678901"},
				{"CATALOGNUMBER", "B1-001"},
				{"ISRC", "us-x1z-25-00001"},
			}
			if format == "mp3" {
				writeIncrementalTestMP3(t, root, path, 43, 1, nil, tags...)
			} else {
				writeFixtureFile(t, root, path, artworkPriorityM4A(nil, tags...))
			}
			runIncrementalTestScan(t, service, logs, root)
			var barcode, catalog string
			if err := fixture.database.QueryRow("SELECT COALESCE(barcode,''),COALESCE(catalog_number,'') FROM releases WHERE source_root_id=$1::uuid", root.ID).Scan(&barcode, &catalog); err != nil {
				t.Fatal(err)
			}
			if barcode != "012345678901" || catalog != "B1-001" {
				t.Errorf("发行条码 / 厂牌编号 = %q / %q，期望 012345678901 / B1-001", barcode, catalog)
			}
			track := presentTrackIDs(t, fixture, root.ID)[path]
			if track == "" {
				t.Fatalf("%s 曲目没有入库", format)
			}
			requireScannedIdentifiers(t, fixture, track, scannedIdentifier{"isrc", "USX1Z2500001", 0})
		})
	}
}

func TestPostgreSQLTrackIdentifiersCommonVersionUpgrade(t *testing.T) {
	if os.Getenv("ROOMUSIC_TEST_SCAN_VERSION_CASE") == t.Name() {
		fixture, root := openVersionScanFixture(t)
		if strconv.Itoa(parser.CommonVersion) != os.Getenv("ROOMUSIC_TEST_SCAN_VERSION_EXPECTED") || parser.CommonVersion != 3 {
			t.Fatalf("覆盖构建的 CommonVersion = %d，期望 3", parser.CommonVersion)
		}
		service, logs := newIncrementalTestService(t, fixture)
		runIncrementalTestScan(t, service, logs, root)
		return
	}
	fixture := newLibraryFixture(t)
	root := insertScannerRoot(t, fixture, "upgrade-identifiers")
	const flac, mp3 = "Album/01.flac", "Other/01.mp3"
	writeLyricsFLAC(t, root, flac, 44, 1, "MUSICBRAINZ_TRACKID="+scanMBID1)
	writeIncrementalTestMP3(t, root, mp3, 45, 1, nil, [2]string{"MUSICBRAINZ_TRACKID", scanMBID2})

	// 子进程用 c3 生成缓存与身份。移除现版代码额外生成的标识事实以构造升级前的数据库状态。
	runScanWithVersionOverlay(t, fixture, root, filepath.Join("parser", "version.go"),
		regexp.MustCompile("(const CommonVersion = )(\\d+)"), -1)
	if _, err := fixture.database.Exec("DELETE FROM track_identifiers WHERE track_id IN (SELECT id FROM tracks WHERE source_root_id=$1::uuid)", root.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.database.Exec("UPDATE library_parse_cache SET observation=observation - 'Identifiers' WHERE root_id=$1::uuid", root.ID); err != nil {
		t.Fatal(err)
	}
	before := readIncrementalTestTracks(t, fixture, root)
	cacheBefore := readIncrementalTestCache(t, fixture, root)
	statesBefore := readIncrementalTestCandidateStates(t, fixture, root)
	if len(before) != 2 || len(cacheBefore) != 2 || len(statesBefore) != 2 {
		t.Fatalf("升级前应有两轨、两条解析缓存与两个候选，实际 %d/%d/%d", len(before), len(cacheBefore), len(statesBefore))
	}
	for _, path := range []string{flac, mp3} {
		if entry := cacheBefore[path]; entry.ParserVersion != "c3.f1" || len(entry.Observation.Identifiers) != 0 {
			t.Fatalf("升级前 %s 应使用 c3.f1 缓存且无标识，实际 %+v", path, entry)
		}
		requireScannedIdentifiers(t, fixture, before[path].ID)
	}

	service, logs := newIncrementalTestService(t, fixture)
	scan, summary := runIncrementalTestScan(t, service, logs, root)
	t.Logf("c3 → c4 增量扫描：解析 %d，缓存命中 %d，写入候选 %d，跳过候选 %d",
		summary.FilesParsed, summary.ParseCacheHits, summary.CandidatesWritten, summary.CandidatesSkipped)
	if summary.FilesParsed != 2 || summary.ParseCacheHits != 0 || summary.CandidatesWritten != 2 || summary.CandidatesSkipped != 0 {
		t.Fatalf("升级后增量扫描应重解析全部文件并重写候选，实际 %+v", summary)
	}
	if after := readIncrementalTestTracks(t, fixture, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("升级前后曲目、发行身份或来源事实不同，前 %+v，后 %+v", before, after)
	}
	cacheAfter := readIncrementalTestCache(t, fixture, root)
	statesAfter := readIncrementalTestCandidateStates(t, fixture, root)
	for key, previous := range statesBefore {
		current := statesAfter[key]
		if current.ReleaseID != previous.ReleaseID || current.Signature == previous.Signature || !current.UpdatedAt.After(previous.UpdatedAt) {
			t.Errorf("升级后候选 %s 应沿用发行并重写签名，前 %+v，后 %+v", key, previous, current)
		}
	}
	for path, value := range map[string]string{flac: scanMBID1, mp3: scanMBID2} {
		previous, current := cacheBefore[path], cacheAfter[path]
		if current.ParserVersion != "c4.f1" || current.SeenScanID != scan.ID || !current.UpdatedAt.After(previous.UpdatedAt) {
			t.Errorf("升级后 %s 的缓存应重新写成 c4.f1，前 %+v，后 %+v", path, previous, current)
		}
		requireScannedIdentifiers(t, fixture, before[path].ID, scannedIdentifier{"mb_recording", value, 0})
	}
	requireNoMissingTracks(t, fixture, root.ID)
}
