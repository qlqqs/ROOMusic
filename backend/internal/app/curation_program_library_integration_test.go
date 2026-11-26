package app

import (
	"slices"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestPostgreSQLCurationProgramLibrary(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	// 用同一轮的真实工作状态、记录及对应核对各个形态。
	type sample struct {
		name, id, track, status, method, remote, recording string
		tracks                                             int
	}
	var cases []sample
	for _, name := range []string{"picard", "barcode", "catalog", "isrc_album", "isrc_single", "cue", "multi_disc", "chinese_search", "untagged"} {
		title, artist := "合成 "+name, "艺人"
		if name == "chinese_search" {
			title = "華語專輯"
			artist = "樂團"
		}
		if name == "untagged" {
			title = "未知专辑"
			artist = "未知艺术家"
		}
		tracks := []string{"present"}
		if name == "multi_disc" {
			tracks = append(tracks, "present")
		}
		id, _ := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: title, artist: artist, tracks: tracks})
		track, trackTitle := externalTrack(t, f.application, id)
		r := programAlbum(title, artist)
		rec := r.Media[0].Tracks[0].RecordingID
		item := sample{name: name, id: id, track: track, status: "matched", remote: r.ID, recording: rec, tracks: len(tracks)}
		switch name {
		case "picard", "multi_disc":
			item.method = "tag_id"
			programTag(t, f.application, track, "mb_release", r.ID)
			programTag(t, f.application, track, "mb_release_group", r.Group.ID)
			programTag(t, f.application, track, "mb_recording", rec)
			if name == "multi_disc" {
				second := uuid.New()
				mustExec(t, f.application, `INSERT INTO media(id,release_id,position,title) VALUES($1,$2,2,'CD 2')`, second, id)
				mustExec(t, f.application, `UPDATE tracks SET medium_id=$2,disc_number=2,position=1 WHERE medium_id IN (SELECT id FROM media WHERE release_id=$1) AND position=2`, id, second)
				r.Media = append(r.Media, sources.Medium{Position: 2, Format: "CD", Tracks: []sources.Track{{Position: 1, RecordingID: uuid.New()}}})
			}
		case "barcode":
			item.method = "barcode"
			mustExec(t, f.application, `UPDATE releases SET barcode=$2 WHERE id=$1`, id, r.Barcode)
			f.source.barcode[r.Barcode] = []sources.Release{{ID: r.ID}}
		case "catalog":
			item.method = "catalog_number"
			mustExec(t, f.application, `UPDATE releases SET label='唱片社',catalog_number='CAT-7' WHERE id=$1`, id)
			f.source.catalog["CAT-7/唱片社"] = []sources.Release{{ID: r.ID}}
		case "isrc_album", "isrc_single":
			item.method = "isrc"
			code := "USABC2300001"
			if name == "isrc_single" {
				code = "USABC2300002"
				mustExec(t, f.application, `UPDATE releases SET candidate_kind='loose_unknown' WHERE id=$1`, id)
			}
			r.Media[0].Tracks[0].ISRCs = []string{code}
			programTag(t, f.application, track, "isrc", code)
			mustExec(t, f.application, `UPDATE tracks SET duration_seconds=100 WHERE id=$1`, track)
			f.source.isrc[code] = []sources.Recording{{ID: rec, Title: trackTitle, LengthMS: r.Media[0].Tracks[0].LengthMS, ISRCs: []string{code}, Releases: []sources.Release{{ID: r.ID}}}}
		case "cue":
			item.method = "disc_id"
			mustExec(t, f.application, `UPDATE tracks SET source_kind='cue_virtual',cue_sheet_path='disc.cue',cue_parent_relative_path='disc.flac',cue_index_frames=0,cue_end_frames=7500 WHERE id=$1`, track)
			mustExec(t, f.application, `UPDATE releases SET media_type='CD' WHERE id=$1`, id)
			f.source.disc = sources.DiscMatch{Exact: true, Releases: []sources.Release{{ID: r.ID}}}
		case "chinese_search":
			item.method = "search"
			item.status = "uncertain"
			r.Title = "华语专辑"
			r.Artists[0].Name = "乐团"
			f.source.search[title] = []sources.Release{r}
		case "untagged":
			item.status = "no_match"
		}
		f.source.releases[r.ID] = r
		cases = append(cases, item)
	}
	f.run(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.assertRelease(t, tc.id, tc.status, tc.method)
			if tc.status != "matched" {
				f.assertCount(t, 0, `SELECT count(*) FROM operations WHERE target_id=$1`, tc.id)
				if tc.name == "chinese_search" {
					f.assertCount(t, 1, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND status='waiting_model' AND conflicts ? 'list_truncated' AND candidates->0->>'external_id'=$2`, tc.id, tc.remote)
				} else {
					f.assertCount(t, 0, `SELECT count(*) FROM curation_pending_items WHERE target_id=$1 AND kind='release_match'`, tc.id)
				}
				return
			}
			f.assertCount(t, 1, `SELECT count(*) FROM operations WHERE operation_type='external_link' AND target_id=$1`, tc.id)
			f.assertCount(t, 1, `SELECT count(*) FROM external_links WHERE object_type='track' AND object_id=$1 AND external_id=$2 AND method=$3`, tc.track, tc.recording, tc.method)
			if tc.name == "isrc_single" {
				f.assertCount(t, 0, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1`, tc.id)
				f.assertCount(t, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1`, tc.id)
				if f.stateFor(t, "artwork", tc.id).Status != "" || f.stateFor(t, "lyrics", tc.track).Status != "done" {
					t.Fatal("单曲的封面或歌词状态错误")
				}
				return
			}
			owner := f.linkOwner(t, tc.id)
			f.assertCount(t, tc.tracks+2, `SELECT count(*) FROM external_links WHERE operation_id=$1`, owner)
			f.assertCount(t, 1, `SELECT count(*) FROM external_links WHERE operation_id=$1 AND object_type='release' AND external_id=$2`, owner, tc.remote)
			f.assertCount(t, 1, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='year' AND year_value=2020`, tc.id)
		})
	}
}

func TestPostgreSQLCurationProgramOnlyEmpty(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	_, r := f.tagged(t)
	f.correct(t, f.a, 1, map[string]any{"label": setChange("人工厂牌")})
	old := f.apply(t, f.a, "fixture", "", map[string]any{"catalog_number": "既有编号"})
	suppressed := f.apply(t, f.a, "fixture", "", map[string]any{"barcode": r.Barcode})
	f.undo(t, suppressed)
	f.run(t)
	f.assertRelease(t, f.a, "matched", "tag_id")
	f.textOwner(t, "catalog_number", "既有编号", old)
	f.assertCount(t, 1, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field='album_artist' AND text_value='Artist A'`, f.a)
	f.assertCount(t, 0, `SELECT count(*) FROM release_auto_values WHERE release_id=$1 AND field IN ('title','year','label','barcode')`, f.a)
	if got := programString(t, f.application, `SELECT label FROM release_effective WHERE id=$1`, f.a); got != "人工厂牌" {
		t.Fatal(got)
	}
}

func TestPostgreSQLCurationProgramGroup(t *testing.T) {
	for _, change := range []string{"revert", "manual_detach"} {
		t.Run(change, func(t *testing.T) {
			f := newProgramFixture(t)
			f.enable(t)
			_, first := f.tagged(t)
			f.run(t)
			// 第一张已建立专辑对应后才添加第二张，避免依赖随机 ID 的排序。
			b, oldGroup := insertGroupedRelease(t, f.application, f.rootID, groupedRelease{title: "第二版", artist: "艺人", tracks: []string{"present"}})
			tr, _ := externalTrack(t, f.application, b)
			second := programAlbum("第二版", "艺人")
			second.Group = first.Group
			f.source.releases[second.ID] = second
			programTag(t, f.application, tr, "mb_release", second.ID)
			programFinishedScan(t, f)
			f.clock.advance(t, time.Minute)
			f.assertRelease(t, b, "matched", "tag_id")
			joined, rev, _ := f.state(t, b)
			if joined != f.groupA {
				t.Fatalf("第二版仍在 %s，期望 %s", joined, f.groupA)
			}
			op := programString(t, f.application, `SELECT id FROM operations WHERE target_id=$1 AND operation_type='release_group_change' AND origin->>'source'='curation'`, b)
			wantGroup, why := oldGroup, "suppressed"
			if change == "revert" {
				f.undo(t, op)
			} else {
				result := f.post(t, &f.admin, b, uuid.New(), detachBody(int(rev)))
				if result.Code != 200 {
					t.Fatal(result.Code, result.Body.String())
				}
				wantGroup, _, _ = f.state(t, b)
				why = "manual"
				requireAPIError(t, f.revert(t, &f.admin, op, uuid.New()), 409, "revert_conflict")
			}
			f.clock.advance(t, time.Minute)
			got, _, _ := f.state(t, b)
			if got != wantGroup || got == joined {
				t.Fatalf("重新归入 %s，期望 %s", got, wantGroup)
			}
			// 用新的识别依据重跑计划，验证不归组的理由。
			mustExec(t, f.application, `UPDATE tracks SET title='重扫后的曲目标题' WHERE id=$1`, tr)
			programFinishedScan(t, f)
			f.clock.advance(t, time.Minute)
			reason := programString(t, f.application, `SELECT result->>'group' FROM curation_work_states WHERE subject='release' AND target_id=$1`, b)
			if reason != why {
				t.Fatalf("归组原因 %q，期望 %q", reason, why)
			}
		})
	}
}

func TestPostgreSQLCurationEvidenceProjection(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	track, _ := f.tagged(t)
	f.correct(t, f.a, 1, map[string]any{"title": setChange("人工标题"), "year": nullChange})
	f.apply(t, f.a, "fixture", "", map[string]any{"label": "自动厂牌"})
	mustExec(t, f.application, `UPDATE releases SET catalog_number='0123456789012',barcode='999' WHERE id=$1`, f.a)
	read := func() catalog.CurationEvidence {
		t.Helper()
		entries, err := catalog.LoadCurationEvidence(t.Context(), f.application.database, []string{f.a, f.b, f.hidden})
		if err != nil || len(entries) != 1 {
			t.Fatalf("可见证据数量 %d %v", len(entries), err)
		}
		return entries[f.a]
	}
	mustExec(t, f.application, `UPDATE tracks SET duration_seconds=100.1 WHERE id=$1`, track)
	e := read()
	if e.Title != "人工标题" || e.Year != 0 || !e.Corrected["year"] || !e.Automatic["label"] || e.Label != "" || !e.CatalogNumberIsMCN || e.CatalogNumber != "" || !slices.Equal(e.Barcodes, []string{"999", "0123456789012"}) || len(e.Tracks) != 1 || e.Tracks[0].DurationSeconds != 100 {
		t.Fatalf("%+v", e)
	}
	original := program.ReleaseDigest(e)
	lyrics := program.LyricsDigest(e, e.Tracks[0])
	mustExec(t, f.application, `UPDATE tracks SET duration_seconds=100.49 WHERE id=$1`, track)
	e = read()
	if program.ReleaseDigest(e) != original || program.LyricsDigest(e, e.Tracks[0]) != lyrics {
		t.Fatal("同一整秒的小数触发重判")
	}
	mustExec(t, f.application, `UPDATE tracks SET duration_seconds=100.6 WHERE id=$1`, track)
	e = read()
	if program.ReleaseDigest(e) == original || program.LyricsDigest(e, e.Tracks[0]) == lyrics {
		t.Fatal("整秒变化没有触发重判")
	}
}
