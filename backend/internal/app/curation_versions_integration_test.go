package app

import (
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestPostgreSQLCurationLegacyReleaseBaseline(t *testing.T) {
	for _, tc := range []struct {
		name             string
		artistBackfilled bool
	}{
		{"已回填艺人对应的旧行补齐新列且零出站", true},
		{"艺人目录准备期间仍建立旧行基线", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newProgramFixture(t)
			f.enable(t)
			_, remote := f.tagged(t)
			f.run(t)
			if tc.artistBackfilled {
				mustExec(t, f.application, `UPDATE curation_work_states SET result=result || '{"artist_links":"done"}'::jsonb
 WHERE subject='release' AND target_id=$1`, f.a)
			} else {
				_, err := catalog.LoadCurationArtistCredits(t.Context(), f.application.database, f.a)
				if !errors.Is(err, catalog.ErrArtistCatalogPreparing) {
					t.Fatalf("应保持艺人目录准备状态，得到 %v", err)
				}
			}
			before := f.assertRelease(t, f.a, "matched", "tag_id")
			if digest := modelDigest(t, f); digest != before.InputDigest {
				t.Fatalf("旧整份指纹应与当前证据吻合，存储 %s，当前 %s", before.InputDigest, digest)
			}
			calls := maps.Clone(f.source.calls)
			owner := f.linkOwner(t, f.a)
			mustExec(t, f.application, `UPDATE curation_work_states SET rule_version=101,
 local_rule_version=NULL,match_rule_version=NULL,identity_digest=NULL,gap_digest=NULL,requery_match_version=NULL
 WHERE subject='release' AND target_id=$1`, f.a)
			f.clock.advance(t, 6*time.Hour)

			after := f.assertRelease(t, f.a, "matched", "tag_id")
			if after.LocalRuleVersion != program.LocalRuleVersion || after.MatchRuleVersion != program.MatchRuleVersion ||
				after.InputDigest != before.InputDigest || after.IdentityDigest == "" || after.IdentityDigest != before.IdentityDigest ||
				after.GapDigest == "" || after.GapDigest != before.GapDigest || after.RequeryMatchVersion != 0 {
				t.Errorf("旧行应补齐两种版本与两种摘要，实际版本 %d/%d、识别摘要 %q、缺口摘要 %q",
					after.LocalRuleVersion, after.MatchRuleVersion, after.IdentityDigest, after.GapDigest)
			}
			if after.Attempts != before.Attempts || after.Generation != before.Generation ||
				string(after.Result) != string(before.Result) || !maps.Equal(calls, f.source.calls) {
				t.Errorf("旧结论及调用应保持，尝试 %d -> %d，代次 %d -> %d，请求 %v -> %v，结论 %s -> %s",
					before.Attempts, after.Attempts, before.Generation, after.Generation, calls, f.source.calls, before.Result, after.Result)
			}
			f.assertCount(t, 1, `SELECT count(*) FROM external_links WHERE object_type='release' AND object_id=$1 AND external_id=$2 AND operation_id=$3`, f.a, remote.ID, owner)
		})
	}

	t.Run("旧展示艺人按已有编号读取一次并回填", func(t *testing.T) {
		f := newProgramFixture(t)
		f.enable(t)
		f.sourceEnabled(t, "lrclib", false)
		f.sourceEnabled(t, "coverartarchive", false)
		track, remote := f.tagged(t)
		const artist = "回填曲目艺人"
		remote.Media[0].Tracks[0].Artists = []sources.Credit{{Name: artist}}
		run := uuid.New()
		mustExec(t, f.application, `INSERT INTO scan_runs(id,status,started_at,finished_at) VALUES($1,'succeeded',now(),now())`, run)
		mustExec(t, f.application, `UPDATE tracks SET artist='未知艺术家' WHERE id=$1`, track)
		mustExec(t, f.application, `INSERT INTO track_observations(track_id,scan_run_id,field_name,source_kind,inferred,value)
 VALUES($1,$2,'artist','display_fallback',true,'未知艺术家')`, track, run)
		before := seedTrackFieldBackfill(t, f, track, remote)

		entries, err := catalog.LoadCurationEvidence(t.Context(), f.application.database, []string{f.a}, f.writer.ArtworkExists)
		requireCurationOK(t, err)
		e := entries[f.a]
		if len(e.Tracks) != 1 || !e.Tracks[0].ArtistPlaceholder || e.Tracks[0].ArtistObservation.Source != "display_fallback" {
			t.Fatalf("新取证应识别展示艺人的占位来源，得到 %+v", e.Tracks)
		}
		// 旧程序对已有的 display_fallback 观察写入 ArtistPlaceholder=false。还原旧指纹的这一个位。
		e.Tracks[0].ArtistPlaceholder = false
		historicalDigest := program.ReleaseDigest(e)
		if historicalDigest == before.InputDigest {
			t.Fatal("夹具未生成历史程序的不同指纹")
		}
		mustExec(t, f.application, `UPDATE curation_work_states SET input_digest=$2,
 result=result || '{"track_fields":"done","artist_links":"done","release_merge":"done"}'::jsonb
 WHERE subject='release' AND target_id=$1`, f.a, historicalDigest)
		owner := f.linkOwner(t, f.a)
		f.run(t)
		after := f.assertRelease(t, f.a, "matched", "tag_id")
		if after.LocalRuleVersion != program.LocalRuleVersion || after.MatchRuleVersion != program.MatchRuleVersion ||
			after.IdentityDigest == "" || after.GapDigest == "" || f.linkOwner(t, f.a) != owner {
			t.Errorf("展示艺人解释升级应建立基线并沿用原对应，实际版本 %d/%d、识别摘要 %q、缺口摘要 %q",
				after.LocalRuleVersion, after.MatchRuleVersion, after.IdentityDigest, after.GapDigest)
		}
		requireProgramTrackFields(t, f, f.a, track, e.Tracks[0].Title, artist, 1)
		f.clock.advance(t, 6*time.Hour)
		if !maps.Equal(f.source.calls, map[string]int{"ReleaseByID": 1}) {
			t.Errorf("展示艺人回填应按已有编号读取一次，搜索为零，实际调用 %v", f.source.calls)
		}
	})

	t.Run("旧匹配仍按已有对应回填曲目", func(t *testing.T) {
		f := newProgramFixture(t)
		f.enable(t)
		f.sourceEnabled(t, "lrclib", false)
		f.sourceEnabled(t, "coverartarchive", false)
		track, _ := externalTrack(t, f.application, f.a)
		seedTrackTitleObservation(t, f, track, "文件名曲目", "filename_fallback")
		remote := programAlbum("Album A", "Artist A")
		remote.Media[0].Tracks[0].Title = "回填曲名"
		f.source.releases[remote.ID] = remote
		before := seedTrackFieldBackfill(t, f, track, remote)
		if before.RuleVersion != 101 || before.IdentityDigest != "" || before.GapDigest != "" {
			t.Fatalf("夹具应为旧格式，得到 %+v", before)
		}
		owner := f.linkOwner(t, f.a)
		f.run(t)
		after := f.assertRelease(t, f.a, "matched", "barcode")
		if after.MatchRuleVersion != program.MatchRuleVersion || after.LocalRuleVersion != program.LocalRuleVersion ||
			after.IdentityDigest == "" || after.GapDigest == "" || after.Generation != before.Generation ||
			after.Attempts != before.Attempts || f.linkOwner(t, f.a) != owner {
			t.Fatalf("回填应保留旧对应和识别代次并建立基线，之前 %+v，之后 %+v", before, after)
		}
		requireProgramTrackFields(t, f, f.a, track, "回填曲名", "Artist A", 1)
		f.clock.advance(t, 6*time.Hour)
		if !maps.Equal(f.source.calls, map[string]int{"ReleaseByID": 1}) {
			t.Fatalf("回填应只按已有对应读取一次，调用 %v", f.source.calls)
		}
	})
}

func TestPostgreSQLCurationGapAndIdentityChanges(t *testing.T) {
	f := newProgramFixture(t)
	f.enable(t)
	f.sourceEnabled(t, "lrclib", false)
	track, _ := externalTrack(t, f.application, f.a)
	mustExec(t, f.application, `UPDATE releases SET year=NULL WHERE id=$1`, f.a)
	f.run(t)
	previous := f.assertRelease(t, f.a, "no_match", "search")
	searches := f.source.calls["SearchReleases"]
	if searches != 1 {
		t.Fatalf("初次搜索 %d 次，期望 1 次", searches)
	}
	for _, tc := range []struct {
		name   string
		change func(t *testing.T)
	}{
		{"封面", func(t *testing.T) {
			mustExec(t, f.application, `INSERT INTO release_artworks(release_id,content_hash,mime_type,width,height,storage_key,source_type)
 VALUES($1,$2,'image/png',10,10,'local-cover.png','embedded')`, f.a, strings.Repeat("a", 64))
		}},
		{"歌词", func(t *testing.T) {
			_, err := f.writer.ApplyLyrics(t.Context(), curation.LyricsRequest{
				TrackID: track, Text: "合成外部歌词", Source: curation.Source{Kind: "source", Provider: "lrclib"},
				ExternalID: "42", Key: uuid.New(), RequestID: "gap-lyrics",
			})
			requireCurationOK(t, err)
		}},
		{"年份", func(t *testing.T) {
			mustExec(t, f.application, `UPDATE releases SET year=2023 WHERE id=$1`, f.a)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.change(t)
			f.clock.advance(t, 6*time.Hour)
			got := f.assertRelease(t, f.a, "no_match", "search")
			if got.IdentityDigest != previous.IdentityDigest || got.GapDigest == previous.GapDigest ||
				got.InputDigest == previous.InputDigest || got.Generation != previous.Generation ||
				got.Attempts != previous.Attempts || f.source.calls["SearchReleases"] != searches {
				t.Errorf("缺口变化应只更新缺口和完整摘要，之前 %+v，之后 %+v，调用 %v", previous, got, f.source.calls)
			}
			previous = got
		})
	}
	for _, tc := range []struct {
		name  string
		query string
		id    string
	}{
		{"标题", `UPDATE releases SET title='新扫描标题' WHERE id=$1`, f.a},
		{"条码", `UPDATE releases SET barcode='123456789012' WHERE id=$1`, f.a},
		{"曲目整秒时长", `UPDATE tracks SET duration_seconds=111 WHERE id=$1`, track},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mustExec(t, f.application, tc.query, tc.id)
			f.clock.advance(t, 6*time.Hour)
			searches++
			got := f.assertRelease(t, f.a, "no_match", "search")
			if got.IdentityDigest == previous.IdentityDigest || got.Generation != previous.Generation+1 ||
				got.Attempts != previous.Attempts+1 || f.source.calls["SearchReleases"] != searches {
				t.Errorf("识别输入变化应触发一次识别，之前 %+v，之后 %+v，调用 %v", previous, got, f.source.calls)
			}
			previous = got
		})
	}
}
