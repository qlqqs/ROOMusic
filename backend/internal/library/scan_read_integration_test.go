package library

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/library/parser"
	"github.com/qlqq/roomusic/backend/internal/library/parser/parsertest"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

func scanReadExec(t *testing.T, f *libraryFixture, query string, args ...any) {
	t.Helper()
	if _, err := f.database.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func scanReadTagJSON(t *testing.T, f *libraryFixture, query string, args ...any) *metadata.ScanTags {
	t.Helper()
	var raw []byte
	if err := f.database.QueryRowContext(t.Context(), query, args...).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var tags *metadata.ScanTags
	if raw != nil {
		if err := json.Unmarshal(raw, &tags); err != nil {
			t.Fatalf("快照 JSON %q：%v", raw, err)
		}
	}
	return tags
}

func TestPostgreSQLScanReadTagsSnapshot(t *testing.T) {
	t.Run("cache_stage_persist_and_replace", func(t *testing.T) {
		f := newLibraryFixture(t)
		root := insertScannerRoot(t, f, "tag-snapshot")
		service, logs := newIncrementalTestService(t, f)
		const relative = "Album/01.flac"
		const synced = "[00:00.000]Line one\n[00:01.000]Line two"
		base := []string{"TITLE=Song", "ARTIST=Artist", "ALBUM=Album", "TRACKNUMBER=7", "DISCNUMBER=2",
			"DATE=1998", "LYRICS=Ordinary lyrics", "CATALOGNUMBER=ABC-123"}
		tagged := append(append([]string{}, base...), "TRACKTOTAL=9", "DISCTOTAL=1", "RELEASEDATE=2024-03-14",
			"COMMENT=First line\nSecond line", "LIVE=0", "REPLAYGAIN_TRACK_GAIN=0 dB", "SYNCED LYRICS="+synced)
		write := func(tags []string) {
			t.Helper()
			writeFixtureFile(t, root, relative, parsertest.FLACWithAudio(t, [16]byte{22}, []byte{1, 2, 3}, tags...))
		}
		scanReadExec(t, f, `CREATE TABLE scan_read_staging_capture (scan_id uuid, relative_path text, payload jsonb);
			CREATE FUNCTION scan_read_capture() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN
			  INSERT INTO scan_read_staging_capture VALUES (NEW.scan_run_id,NEW.relative_path,NEW.observation);
			  RETURN NEW;
			END $$;
			CREATE TRIGGER scan_read_capture AFTER INSERT ON scan_observations FOR EACH ROW EXECUTE FUNCTION scan_read_capture()`)

		write(tagged)
		first, _ := runIncrementalTestScan(t, service, logs, root)
		track := readIncrementalTestTracks(t, f, root)[relative]
		zero, no := 0.0, false
		want := &metadata.ScanTags{
			TrackTotal: 9, DiscTotal: 2, ReleaseDate: &metadata.ScanDate{Year: 2024, Month: 3, Day: 14},
			Live: &no, TrackGain: &zero, SyncedLyrics: synced, Comments: []metadata.ScanComment{{Key: "COMMENT", Value: "First line\nSecond line"}},
		}
		persistedSQL := `SELECT scan_tags FROM tracks WHERE id=$1`
		assertScanTags := func(scan Scan, checkCache bool) {
			t.Helper()
			if staged := scanReadTagJSON(t, f, `SELECT payload->'scan_tags' FROM scan_read_staging_capture WHERE scan_id=$1 AND relative_path=$2`, scan.ID, relative); !reflect.DeepEqual(staged, want) {
				t.Errorf("暂存快照 = %+v，期望 %+v", staged, want)
			}
			if stored := scanReadTagJSON(t, f, persistedSQL, track.ID); !reflect.DeepEqual(stored, want) {
				t.Errorf("曲目快照 = %+v，期望 %+v", stored, want)
			}
			entry := readIncrementalTestCache(t, f, root)[relative]
			if !reflect.DeepEqual(entry.Observation.ScanTags, want) || entry.SeenScanID != scan.ID {
				t.Errorf("缓存快照 = %+v，期望 %+v，seen = %s", entry.Observation.ScanTags, want, entry.SeenScanID)
			}
			if checkCache {
				assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE id=$1 AND scan_tags ? 'live' AND NOT (scan_tags ? 'single')`, 1, track.ID)
			}
		}
		assertScanTags(first, true)
		cached, stats := runIncrementalTestScan(t, service, logs, root)
		if stats.ParseCacheHits != 1 || stats.FilesParsed != 0 {
			t.Errorf("快照缓存未命中：%+v", stats)
		}
		assertScanTags(cached, true)
		assertOldFields := func() {
			t.Helper()
			entries, err := catalog.LoadCurationEvidence(t.Context(), f.database, []string{track.ReleaseID})
			if err != nil {
				t.Fatal(err)
			}
			if e := entries[track.ReleaseID]; e.CatalogNumber != "ABC-123" || len(e.Tracks) != 1 || e.Tracks[0].Position != 7 {
				t.Errorf("整理编号被旁存标签改变：编号 %q，曲目 %+v", e.CatalogNumber, e.Tracks)
			}
			assertDatabaseCount(t, f, `SELECT count(*) FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id
				WHERE t.id=$1 AND r.year=1998 AND t.lyrics='Ordinary lyrics' AND t.disc_number=2 AND t.position=7
				AND t.codec='flac' AND t.sample_rate=$2 AND t.bit_depth=$3 AND t.channels=$4 AND t.duration_seconds=$5`,
				1, track.ID, parsertest.FLACSampleRate, parsertest.FLACBitDepth, parsertest.FLACChannels, parsertest.FLACTotalSamples/parsertest.FLACSampleRate)
		}
		assertOldFields()

		write(base)
		removed, stats := runIncrementalTestScan(t, service, logs, root)
		if stats.FilesParsed != 1 || stats.CandidatesWritten != 1 {
			t.Errorf("移除标签未使快照重写：%+v", stats)
		}
		want = &metadata.ScanTags{ReleaseDate: &metadata.ScanDate{Year: 1998}}
		assertScanTags(removed, false)
		assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE id=$1 AND NOT (scan_tags ?| array['live','track_total','disc_total','synced_lyrics','comments','track_gain'])`, 1, track.ID)
		assertOldFields()
	})

	t.Run("cue_uses_parent_snapshot", func(t *testing.T) {
		f := newLibraryFixture(t)
		root := insertScannerRoot(t, f, "cue-snapshot")
		service, logs := newIncrementalTestService(t, f)
		const relative = "Album/image.flac"
		writeFixtureFile(t, root, relative, parsertest.FLACWithAudio(t, [16]byte{24}, []byte{1, 2, 3},
			"TITLE=Image", "ALBUM=Cue Album", "ARTIST=Artist", "TRACKTOTAL=9", "COMMENT=Parent comment", "LYRICS=Parent lyrics"))
		writeFixtureFile(t, root, "Album/00.cue", filterCueDocument("image.flac"))
		runIncrementalTestScan(t, service, logs, root)
		assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE source_root_id=$1 AND source_status='present'
			AND source_kind='cue_virtual' AND cue_parent_relative_path=$2 AND lyrics IS NULL
			AND scan_tags @> '{"track_total":9,"comments":[{"key":"COMMENT","value":"Parent comment"}]}'`, 2, root.ID, relative)
	})

	t.Run("budget_and_transaction_rollback", func(t *testing.T) {
		f := newLibraryFixture(t)
		root := insertScannerRoot(t, f, "snapshot-rollback")
		service, logs := newIncrementalTestService(t, f)
		const relative = "Album/01.flac"
		write := func(title string, comments ...string) {
			t.Helper()
			tags := append([]string{"TITLE=" + title, "ARTIST=Artist", "ALBUM=Album", "TRACKNUMBER=1"}, comments...)
			writeFixtureFile(t, root, relative, parsertest.FLACWithAudio(t, [16]byte{21}, []byte{1, 2, 3}, tags...))
		}
		write("Original", "COMMENT=old value")
		runIncrementalTestScan(t, service, logs, root)
		track := readIncrementalTestTracks(t, f, root)[relative]
		before := scanReadTagJSON(t, f, `SELECT scan_tags FROM tracks WHERE id=$1`, track.ID)

		scanReadExec(t, f, `CREATE FUNCTION scan_read_reject_observation() RETURNS trigger LANGUAGE plpgsql AS $$
			BEGIN RAISE EXCEPTION 'synthetic candidate write failure'; END $$;
			CREATE TRIGGER scan_read_reject_observation BEFORE INSERT ON track_observations
			FOR EACH ROW EXECUTE FUNCTION scan_read_reject_observation()`)
		part := strings.Repeat("a", maxParseCacheBytes/2+16)
		write("Updated", "COMMENT="+part, "DESCRIPTION="+part)
		failed := runFilterLifecycleScan(t, service)
		requireFilterScan(t, failed, "incomplete", 0)
		requireDiagnosticCount(t, f, failed.ID, "catalog_write_failure", 1)
		if after := scanReadTagJSON(t, f, `SELECT scan_tags FROM tracks WHERE id=$1`, track.ID); !reflect.DeepEqual(before, after) {
			t.Errorf("回滚未保留旧快照：之前 %+v，之后 %+v", before, after)
		}
		assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE id=$1 AND title='Original' AND source_status='present'`, 1, track.ID)
		scanReadExec(t, f, `DROP TRIGGER scan_read_reject_observation ON track_observations`)
		_, summary := runIncrementalTestScan(t, service, logs, root)
		if summary.FilesParsed != 1 || summary.ParseCacheWrites != 0 || summary.CandidatesWritten != 1 {
			t.Errorf("超预算快照应跳过解析缓存且照常入库：%+v", summary)
		}
		assertDatabaseCount(t, f, `SELECT count(*) FROM library_parse_cache WHERE root_id=$1`, 0, root.ID)
		var title string
		if err := f.database.QueryRow(`SELECT title FROM tracks WHERE id=$1`, track.ID).Scan(&title); err != nil || title != "Updated" {
			t.Errorf("重试标题=%q，错误 %v", title, err)
		}
		got := scanReadTagJSON(t, f, `SELECT scan_tags FROM tracks WHERE id=$1`, track.ID)
		if got == nil || len(got.Comments) != 2 || got.Comments[0].Value != part || got.Comments[1].Value != part {
			t.Errorf("超预算注释未完整入库")
		}
	})
}

func TestPostgreSQLScanReadCommonVersionUpgrade(t *testing.T) {
	f := newLibraryFixture(t)
	root := insertScannerRoot(t, f, "common-version-upgrade")
	service, logs := newIncrementalTestService(t, f)
	for i := 1; i <= 3; i++ {
		writeFixtureFile(t, root, fmt.Sprintf("Album/%02d.flac", i), parsertest.FLACWithAudio(t, [16]byte{byte(i)}, bytes.Repeat([]byte{byte(i)}, 32),
			fmt.Sprintf("TITLE=Song %d", i), "ALBUM=Ordinary Album", "ARTIST=Artist", "ALBUMARTIST=Artist",
			fmt.Sprintf("TRACKNUMBER=%d/3", i), "DISCNUMBER=1", "DATE=2001"))
	}
	seed, _ := runIncrementalTestScan(t, service, logs, root)
	tracks := readIncrementalTestTracks(t, f, root)
	track := tracks["Album/01.flac"]
	if len(tracks) != 3 || track.ID == "" {
		t.Fatalf("历史曲目夹具不完整：%+v", tracks)
	}
	layout := scanReadAlbumLayout(t, f, track.ReleaseID)
	var groupID string
	var revision int64
	if err := f.database.QueryRow(`SELECT group_id::text,edit_revision FROM releases WHERE id=$1`, track.ReleaseID).Scan(&groupID, &revision); err != nil {
		t.Fatal(err)
	}
	userID, linkID, externalID := uuid.New(), uuid.New(), uuid.New()
	scanReadExec(t, f, `UPDATE library_parse_cache SET parser_version='c5.f1',observation=observation-'Format'-'ScanTags' WHERE root_id=$1`, root.ID)
	scanReadExec(t, f, `UPDATE tracks SET scan_tags=NULL WHERE source_root_id=$1`, root.ID)
	scanReadExec(t, f, `UPDATE scan_candidate_states SET signature=repeat('0',64) WHERE root_id=$1`, root.ID)
	assertDatabaseCount(t, f, `SELECT count(*) FROM library_parse_cache WHERE root_id=$1 AND parser_version='c5.f1' AND seen_scan_id=$2 AND NOT (observation ? 'Format')`, 3, root.ID, seed.ID)

	scanReadExec(t, f, `INSERT INTO users(id,username,password_hash,role) VALUES($1,'scan-read-upgrade','test-only','admin')`, userID)
	_, err := operations.Run(t.Context(), f.database, operations.Spec{
		ActorID: userID, Type: operations.TypeReleaseCorrection, Key: uuid.New(), Fingerprint: "scan-read-upgrade",
		RequestID: "scan-read-upgrade",
	}, governance.CorrectionExecutor(governance.CorrectionRequest{
		ReleaseID: track.ReleaseID, ExpectedRevision: revision, Changes: map[string]governance.CorrectionChange{
			"title": {Mode: catalog.CorrectionSet, Text: "Manually corrected album"},
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	scanReadExec(t, f, `INSERT INTO personal_favorite_state(user_id,revision) VALUES($1,10)`, userID)
	scanReadExec(t, f, `INSERT INTO favorite_tracks(user_id,track_id) VALUES($1,$2)`, userID, track.ID)
	scanReadExec(t, f, `INSERT INTO favorite_albums(user_id,group_id,saved_title,saved_artist) VALUES($1,$2,'Saved title','Saved artist')`, userID, groupID)
	scanReadExec(t, f, `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence)
		VALUES($1,'musicbrainz','release',$2,$3,'manual','high')`, linkID, track.ReleaseID, externalID)

	for i := range 2 {
		scan, stats := runIncrementalTestScan(t, service, logs, root)
		if i == 0 && (stats.FilesParsed != 3 || stats.ParseCacheHits != 0 || stats.ParseCacheWrites != 3 || stats.CandidatesWritten != 1) ||
			i == 1 && (stats.FilesParsed != 0 || stats.ParseCacheHits != 3 || stats.ParseCacheWrites != 0 || stats.CandidatesSkipped != 1) {
			t.Errorf("第 %d 次升版扫描统计 %+v", i+1, stats)
		}
		for path, e := range readIncrementalTestCache(t, f, root) {
			if e.ParserVersion != parser.CacheVersion(".flac") || e.ParserVersion != "c6.f1" || e.SeenScanID != scan.ID {
				t.Errorf("第 %d 次扫描 %s 的真实格式缓存 = %+v", i+1, path, e)
			}
		}
		if after := readIncrementalTestTracks(t, f, root); !reflect.DeepEqual(after, tracks) {
			t.Errorf("升版替换曲目或来源身份：之前 %+v，之后 %+v", tracks, after)
		}
		if after := scanReadAlbumLayout(t, f, track.ReleaseID); after != layout {
			t.Errorf("升版改变专辑、介质或曲号：之前 %s，之后 %s", layout, after)
		}
		assertDatabaseCount(t, f, `SELECT count(*) FROM release_effective WHERE id=$1 AND group_id=$2 AND title='Manually corrected album' AND year=2001`, 1, track.ReleaseID, groupID)
		assertDatabaseCount(t, f, `SELECT count(*) FROM favorite_tracks WHERE user_id=$1 AND track_id=$2`, 1, userID, track.ID)
		assertDatabaseCount(t, f, `SELECT count(*) FROM favorite_albums WHERE user_id=$1 AND group_id=$2 AND saved_title='Saved title'`, 1, userID, groupID)
		assertDatabaseCount(t, f, `SELECT revision FROM personal_favorite_state WHERE user_id=$1`, 10, userID)
		assertDatabaseCount(t, f, `SELECT count(*) FROM external_links WHERE id=$1 AND object_id=$2 AND external_id=$3`, 1, linkID, track.ReleaseID, externalID)
		assertDatabaseCount(t, f, `SELECT count(*) FROM tracks WHERE source_root_id=$1 AND scan_tags @> '{"track_total":3}'`, 3, root.ID)
	}
}

func scanReadAlbumLayout(t *testing.T, f *libraryFixture, releaseID string) string {
	t.Helper()
	var layout string
	err := f.database.QueryRow(`SELECT jsonb_agg(jsonb_build_array(r.id,r.group_id,r.title,m.id,m.position,t.id,t.position,t.disc_number)
		ORDER BY m.position,t.position)::text FROM releases r JOIN media m ON m.release_id=r.id JOIN tracks t ON t.medium_id=m.id
		WHERE r.id=$1 AND t.source_status='present'`, releaseID).Scan(&layout)
	if err != nil {
		t.Fatal(err)
	}
	return layout
}
