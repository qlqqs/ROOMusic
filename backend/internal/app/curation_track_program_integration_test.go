package app

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/lrclib"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

func seedTrackTitleObservation(t *testing.T, f *programFixture, track, title, source string) {
	t.Helper()
	run := uuid.New()
	mustExec(t, f.application, `INSERT INTO scan_runs(id,status,started_at,finished_at) VALUES($1,'succeeded',now(),now())`, run)
	mustExec(t, f.application, `UPDATE tracks SET title=$2 WHERE id=$1`, track, title)
	mustExec(t, f.application, `
		INSERT INTO track_observations(track_id,scan_run_id,field_name,source_kind,inferred,value)
		VALUES($1,$2,'title',$3,true,$4)`, track, run, source, title)
}

func requireProgramTrackFields(t *testing.T, f *programFixture, release, track, title, artist string, written int) {
	t.Helper()
	f.assertCount(t, 1, `SELECT count(*) FROM track_effective WHERE id=$1 AND title=$2 AND artist=$3`, track, title, artist)
	f.assertCount(t, written, `
		SELECT count(*) FROM track_auto_values v JOIN operations o ON o.id=v.operation_id
		WHERE v.track_id=$1 AND o.target_id=$2 AND o.operation_type='track_auto_correction'
		  AND o.origin->>'provider_kind'='source' AND o.origin->>'provider'='musicbrainz'`, track, release)
}

func TestPostgreSQLCurationProgramTrackFields(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	album, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{
		title: "合成专辑", artist: "专辑艺人", tracks: []string{"present", "present", "present", "present"},
	})
	remote := programAlbum("合成专辑", "专辑艺人")
	first := remote.Media[0].Tracks[0]
	tracks := make([]string, 4)
	for i := range tracks {
		tracks[i] = programString(t, f.application, `
			SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1 AND t.position=$2`, album, i+1)
	}
	remote.Media[0].Tracks = nil
	for i, track := range tracks {
		remoteTrack := first
		remoteTrack.Position, remoteTrack.ID, remoteTrack.RecordingID = i+1, uuid.New(), uuid.New()
		remoteTrack.Title = fmt.Sprintf("外部曲名 %d", i+1)
		remoteTrack.Artists = []sources.Credit{{Name: fmt.Sprintf("外部艺人 %d", i+1)}}
		remote.Media[0].Tracks = append(remote.Media[0].Tracks, remoteTrack)
		mustExec(t, f.application, `UPDATE tracks SET artist='未知艺术家',duration_seconds=100 WHERE id=$1`, track)
	}
	seedTrackTitleObservation(t, f, tracks[0], "未知曲目", "default_fallback")
	seedTrackTitleObservation(t, f, tracks[1], "02-文件名", "filename_fallback")
	seedTrackTitleObservation(t, f, tracks[2], "03-文件名", "filename_fallback")
	seedTrackTitleObservation(t, f, tracks[3], "04-文件名", "filename_fallback")
	correctTrackForCuration(t, f.curationFixture, tracks[2], 1, map[string]any{"title": setChange("人工曲名")})
	automatic := applyTrackAuto(t, f.curationFixture, trackAutoRequest(tracks[2], album, "fixture", "", map[string]any{"artist": "既有自动艺人"}))
	suppressed := applyTrackAuto(t, f.curationFixture, trackAutoRequest(tracks[3], album, "fixture", "", map[string]any{"title": "外部曲名 4", "artist": "外部艺人 4"}))
	f.undo(t, suppressed)
	programTag(t, f.application, tracks[0], "mb_release", remote.ID)
	f.source.releases[remote.ID] = remote

	single, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "合成单曲", artist: "未知艺术家", tracks: []string{"present"}})
	singleTrack, _ := externalTrack(t, f.application, single)
	mustExec(t, f.application, `UPDATE releases SET candidate_kind='loose_unknown' WHERE id=$1`, single)
	mustExec(t, f.application, `UPDATE tracks SET duration_seconds=100 WHERE id=$1`, singleTrack)
	seedTrackTitleObservation(t, f, singleTrack, "未知曲目", "default_fallback")
	recording := sources.Recording{ID: uuid.New(), Title: "单曲曲名", Artists: []sources.Credit{{Name: "单曲艺人"}}, LengthMS: first.LengthMS}
	programTag(t, f.application, singleTrack, "mb_recording", recording.ID)
	f.source.recordings[recording.ID] = recording
	f.sourceEnabled(t, "lrclib", false)

	f.run(t)
	for _, release := range []string{album, single} {
		f.assertRelease(t, release, "matched", "tag_id")
		f.assertCount(t, 1, `SELECT count(*) FROM curation_work_states WHERE subject='release' AND target_id=$1 AND result->>'track_fields'='done'`, release)
	}
	requireProgramTrackFields(t, f, album, tracks[0], "外部曲名 1", "外部艺人 1", 2)
	requireProgramTrackFields(t, f, album, tracks[1], "外部曲名 2", "外部艺人 2", 2)
	requireProgramTrackFields(t, f, album, tracks[2], "人工曲名", "既有自动艺人", 0)
	requireTrackAutoOwner(t, f.curationFixture, tracks[2], "artist", "既有自动艺人", automatic)
	requireTrackAutoOwner(t, f.curationFixture, tracks[2], "title", "", "")
	requireProgramTrackFields(t, f, album, tracks[3], "04-文件名", "未知艺术家", 0)
	f.assertCount(t, 0, `SELECT count(*) FROM track_auto_values WHERE track_id=$1`, tracks[3])
	requireProgramTrackFields(t, f, single, singleTrack, "单曲曲名", "单曲艺人", 2)
	f.assertCount(t, 1, `SELECT count(*) FROM external_links WHERE source='musicbrainz' AND object_type='track' AND object_id=$1 AND external_id=$2`, singleTrack, recording.ID)
}

func seedTrackFieldBackfill(t *testing.T, f *programFixture, track string, r sources.Release) curation.WorkState {
	t.Helper()
	curationCApplyLinks(t, f.curationFixture, curationCLinkRequest(f.a, "",
		curationCLink("release", f.a, r.ID), curationCLink("track", track, r.Media[0].Tracks[0].RecordingID)))
	state := curation.WorkState{
		Subject: "release", TargetID: f.a, Status: "matched", RuleVersion: program.StoredRuleVersion,
		InputDigest: modelDigest(t, f), Generation: 7, Attempts: 3, Result: json.RawMessage(`{"mode":"album","method":"barcode","conflicts":[]}`),
	}
	requireCurationOK(t, curation.SaveWorkState(t.Context(), f.application.database, state))
	return f.stateFor(t, "release", f.a)
}

func TestPostgreSQLCurationTrackFieldsBackfillOnce(t *testing.T) {
	for _, tc := range []struct {
		name, marker string
		failure      error
	}{
		{name: "done", marker: "done"},
		{name: "not_found", marker: "unavailable", failure: sources.ErrNotFound},
		{name: "try_later", failure: sources.ErrTryLater},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProgramFixture(t)
			f.enable(t)
			f.sourceEnabled(t, "lrclib", false)
			track, _ := externalTrack(t, f.application, f.a)
			seedTrackTitleObservation(t, f, track, "存量文件名", "filename_fallback")
			r := programAlbum("Album A", "Artist A")
			r.Media[0].Tracks[0].Title = "补做曲名"
			f.source.releases[r.ID] = r
			before := seedTrackFieldBackfill(t, f, track, r)
			f.source.hook = func(method string) error {
				if method == "ReleaseByID" {
					if tc.marker != "" && f.source.callCount(method) > 1 {
						// 多余读取立即返回，让工作者结束本轮后直接核对调用次数。
						return sources.ErrTryLater
					}
					return tc.failure
				}
				return nil
			}

			f.run(t)
			after := f.stateFor(t, "release", f.a)
			if f.source.calls["ReleaseByID"] != 1 || f.source.calls["SearchReleases"] != 0 {
				t.Fatalf("存量补做应只读取一次已有对应，调用 %v", f.source.calls)
			}
			if tc.marker == "" {
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("暂时失败改变工作状态，之前 %+v，之后 %+v", before, after)
				}
				f.assertCount(t, 0, `SELECT count(*) FROM track_auto_values WHERE track_id=$1`, track)
				return
			}
			var result map[string]any
			requireCurationOK(t, json.Unmarshal(after.Result, &result))
			want := map[string]any{"mode": "album", "method": "barcode", "conflicts": []any{}, "track_fields": tc.marker, "release_merge": "done"}
			if after.Status != before.Status || after.Generation != before.Generation || after.Attempts != before.Attempts ||
				after.RuleVersion != before.RuleVersion || after.InputDigest != before.InputDigest || !reflect.DeepEqual(result, want) {
				t.Fatalf("补做结果 %+v，状态之前 %+v，之后 %+v", result, before, after)
			}
			if tc.marker == "done" {
				requireProgramTrackFields(t, f, f.a, track, "补做曲名", "Artist A", 1)
			} else {
				f.assertCount(t, 0, `SELECT count(*) FROM track_auto_values WHERE track_id=$1`, track)
			}
			// 完整轮询会再次读到扫描层的待补标题。
			f.clock.advance(t, 7*time.Hour)
			if again := f.stateFor(t, "release", f.a); f.source.calls["ReleaseByID"] != 1 || !reflect.DeepEqual(again, after) {
				t.Fatalf("有 track_fields 标记后又补做，外部调用 %v，状态 %+v -> %+v", f.source.calls, after, again)
			}
		})
	}
}

type trackLyricsProgramSource struct {
	*programSource
	queries []sources.LyricsQuery
}

func (s *trackLyricsProgramSource) LyricsFor(ctx context.Context, q sources.LyricsQuery) (sources.Lyrics, error) {
	s.queries = append(s.queries, q)
	if q.Artist != "补全歌词艺人" {
		return sources.Lyrics{}, sources.ErrNotFound
	}
	return s.programSource.LyricsFor(ctx, q)
}

func TestPostgreSQLCurationLyricsUsesEffectiveArtist(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	track, scannedTitle := externalTrack(t, f.application, f.a)
	mustExec(t, f.application, `UPDATE tracks SET artist='未知艺术家',duration_seconds=100 WHERE id=$1`, track)
	r := programAlbum("Album A", "Artist A")
	r.Media[0].Tracks[0].Title = scannedTitle
	r.Media[0].Tracks[0].Artists = []sources.Credit{{Name: "补全歌词艺人"}}
	f.source.releases[r.ID] = r
	seedTrackFieldBackfill(t, f, track, r)
	entries, err := catalog.LoadCurationEvidence(t.Context(), f.application.database, []string{f.a}, f.writer.ArtworkExists)
	requireCurationOK(t, err)
	e := entries[f.a]
	previous := curation.WorkState{Subject: "lyrics", TargetID: track, Status: "no_match", RuleVersion: program.StoredRuleVersion,
		InputDigest: program.LyricsDigest(e, e.Tracks[0]), Generation: 5, Attempts: 2, Result: json.RawMessage(`{}`)}
	requireCurationOK(t, curation.SaveWorkState(t.Context(), f.application.database, previous))

	lyrics := &trackLyricsProgramSource{programSource: f.source}
	f.gate = curation.NewSources(f.application.database,
		curation.SourceRegistration{Declaration: musicbrainz.Declaration(), Releases: f.source, Recordings: f.source},
		curation.SourceRegistration{Declaration: lrclib.Declaration(), Lyrics: lyrics}).WithScanCheck(f.scanCheck)
	f.run(t)

	requireProgramTrackFields(t, f, f.a, track, scannedTitle, "补全歌词艺人", 1)
	b3RequireCount(t, f.application, 1, `SELECT count(*) FROM tracks WHERE id=$1 AND artist='未知艺术家'`, track)
	want := []sources.LyricsQuery{{Title: scannedTitle, Artist: "补全歌词艺人", Album: "Album A", DurationSeconds: 100}}
	if !reflect.DeepEqual(lyrics.queries, want) {
		t.Fatalf("歌词请求 %+v，期望 %+v", lyrics.queries, want)
	}
	after := f.stateFor(t, "lyrics", track)
	if after.Status != "done" || after.Generation != previous.Generation+1 || after.InputDigest == previous.InputDigest {
		t.Fatalf("补全艺人后的歌词状态 %+v", after)
	}
	f.assertCount(t, 1, `SELECT count(*) FROM track_external_lyrics WHERE track_id=$1 AND source='lrclib' AND external_id='7' AND lyrics='合成歌词'`, track)
	requireExternalLyricsHTTP(t, f.application, &f.admin, track, "online", "plain", []map[string]any{{"start_ms": nil, "text": "合成歌词"}})
}
