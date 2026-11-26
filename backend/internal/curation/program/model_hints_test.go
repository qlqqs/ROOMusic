package program

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestJudgeShortAlbumNameHint(t *testing.T) {
	for _, scenario := range []string{"全轨吻合", "曲名反证", "时长反证", "时长缺失", "位置推断", "名字线索不吻合", "非唯一候选"} {
		t.Run(scenario, func(t *testing.T) {
			e, release := b3Album(4)
			e.Title = "本地异写"
			e.Authenticity = map[string]metadata.Authenticity{"title": metadata.Real, "artist": metadata.Real}
			release.Complete = true
			release.Media[0].TrackCount = 4
			for i, track := range e.Tracks {
				e.Tracks[i].Authenticity = map[string]metadata.Authenticity{"title": metadata.Real}
				release.Media[0].Tracks[i].Title = track.Title
			}
			hint := curation.JudgeGroup{Name: curation.JudgeName{Release: release.Title}}
			switch scenario {
			case "曲名反证":
				release.Media[0].Tracks[2].Title = "另一首完全不同的曲目"
			case "时长反证":
				length := int64(140000)
				release.Media[0].Tracks[2].LengthMS = &length
			case "时长缺失":
				release.Media[0].Tracks[2].LengthMS = nil
			case "位置推断":
				e.Tracks[2].PositionInferred = true
			case "名字线索不吻合":
				hint.Name.Release = "模型猜测的第三种写法"
			}
			rows := []sources.Release{release}
			if scenario == "非唯一候选" {
				other := release
				other.ID = "another-edition"
				rows = append(rows, other)
			}
			finder := &programSearchProbe{releasesFor: func(curation.ReleaseSearch) []sources.Release { return rows }}
			w := &Worker{}
			got, err := w.lookupHint(t.Context(), e, "album", hint, MatchSnapshot{}, nil, finder, finder)
			if err != nil || len(finder.calls) != 1 {
				t.Fatalf("线索查证错误=%v，查询=%v", err, finder.calls)
			}
			if scenario == "全轨吻合" {
				if got.Status != "matched" || got.Rule != "model_title_tracks" || got.Release == nil || got.Release.ID != release.ID || len(got.Pairs) != 4 {
					t.Fatalf("唯一候选应按四轨独立证据认出版：%+v", got)
				}
				return
			}
			if got.Status == "matched" || got.Release != nil || len(got.Pairs) != 0 {
				t.Fatalf("同艺人、同曲数不足以通过此反例：%+v", got)
			}
		})
	}
}

func TestJudgeIdentifierRequiresExactLocalOccurrence(t *testing.T) {
	e := catalog.CurationEvidence{Tracks: []catalog.CurationTrack{{
		RelativePath: "sample/5901234123457.flac",
		LocalTags:    map[string]string{"label": "SPLIT-"},
		Comments:     []metadata.ScanComment{{Key: "COMMENT", Value: "catalog CAT-472"}, {Key: "NOTE", Value: "42"}},
	}}}
	for _, tc := range []struct{ name, kind, value, query string }{
		{"注释目录号", "catalog_number", "CAT-472", "search:///0//CAT-472/"},
		{"文件名条码", "barcode", "5901234123457", "search:///0/5901234123457//"},
		{"仅在候选", "catalog_number", "MB-907", ""},
		{"模型记忆", "barcode", "4006381333931", ""},
		{"不同大小写", "catalog_number", "cat-472", ""},
		{"全角同形", "catalog_number", "ＣAT-472", ""},
		{"跨字段", "catalog_number", "SPLIT-42", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hint curation.JudgeGroup
			hint.Identifier.Kind, hint.Identifier.Value = tc.kind, tc.value
			prior := []Candidate{{ExternalID: "candidate", CatalogNumbers: []string{"MB-907"}}}
			probe := &programSearchProbe{}
			got, err := (&Worker{}).lookupHint(t.Context(), e, "album", hint, MatchSnapshot{}, prior, probe, probe)
			var want []string
			if tc.query != "" {
				want = []string{tc.query}
			}
			if err != nil || !slices.Equal(probe.calls, want) || got.Status == "matched" {
				t.Fatalf("查询=%v，期望 %v，状态=%s，错误=%v", probe.calls, want, got.Status, err)
			}
		})
	}
}

func TestJudgeHintsKeepAmbiguousCandidates(t *testing.T) {
	for _, sameGroup := range []bool{true, false} {
		name := "跨组"
		if sameGroup {
			name = "同组"
		}
		t.Run(name, func(t *testing.T) {
			e, first := b3Album(4)
			e.Year, e.Edition, e.MediaType = 2002, "JP Deluxe", "CD"
			first.Date, first.Country, first.Complete = "2002", "JP", true
			first.Title += " Deluxe"
			first.Media[0].TrackCount = 4
			for i := range e.Tracks {
				first.Media[0].Tracks[i].Title = e.Tracks[i].Title
			}
			e.Title = first.Title
			second := first
			second.ID, second.Date, second.Country = "second", "2003", "GB"
			if !sameGroup {
				second.Group.ID = "second-group"
			}
			rows := releaseCandidates("search", []sources.Release{first, second})
			var hint curation.JudgeGroup
			hint.Version.Year, hint.Version.Country, hint.Version.Edition, hint.Version.Medium = "2002", "JP", "Deluxe", "CD"
			probe := &programSearchProbe{}
			w := &Worker{}
			ambiguous, err := w.lookupHint(t.Context(), e, "album", curation.JudgeGroup{}, MatchSnapshot{}, rows, probe, probe)
			if err != nil {
				t.Fatal(err)
			}
			// 用只筛既有候选的版本组验证 B2 歧义和同组筛版。
			got, err := w.lookupHint(t.Context(), e, "album", hint, MatchSnapshot{}, rows, probe, probe)
			if err != nil || len(probe.calls) != 0 {
				t.Fatalf("版本线索应只筛现有完整候选，查询=%v 错误=%v", probe.calls, err)
			}
			if ambiguous.Status == "matched" {
				t.Fatalf("没给线索却写入：%+v", ambiguous)
			}
			if sameGroup {
				if got.Status != "matched" || got.Release == nil || got.Release.ID != first.ID {
					t.Fatalf("同组应按原文版本线索区分：%+v", got)
				}
			} else {
				wantMatch(t, got, "uncertain", "ambiguous_editions")
				if len(got.Candidates) != 2 || got.Candidates[0].ExternalID != first.ID || got.Candidates[1].ExternalID != second.ID {
					t.Fatalf("应保留两个通过核对的候选：%+v", got.Candidates)
				}
			}
		})
	}
}

func TestJudgeHintUsesOnlyOfferedArtistAndProgramTypes(t *testing.T) {
	e, _ := b3Album(4)
	const listed = "10000000-0000-4000-8000-000000000001"
	const unlisted = "10000000-0000-4000-8000-000000000002"
	snapshot := MatchSnapshot{Artists: map[string]sources.Artist{listed: {}}}
	for _, tc := range []struct {
		name string
		hint curation.JudgeGroup
		want []string
	}{
		{"已提示的同名艺人", curation.JudgeGroup{ArtistID: listed}, []string{"search:合成专辑/乐队/" + listed + "/4///", "search:合成专辑/乐队/" + listed + "/0///"}},
		{"未提示的同名艺人", curation.JudgeGroup{ArtistID: unlisted}, nil},
		{"播客", curation.JudgeGroup{AlbumType: "podcast", Name: curation.JudgeName{Release: "仍不查这个名字"}}, nil},
		{"有声书", curation.JudgeGroup{AlbumType: "audiobook", Name: curation.JudgeName{Release: "仍不查这个名字"}}, nil},
		{"合辑", curation.JudgeGroup{AlbumType: "compilation"}, []string{"search:合成专辑/乐队/" + variousArtistsID + "/4///", "search:合成专辑/乐队/" + variousArtistsID + "/0///"}},
		{"古典作品与作曲者", curation.JudgeGroup{Name: curation.JudgeName{Work: "合成组曲", Composer: "合成作曲家"}}, []string{"search:合成组曲/合成作曲家//4///", "search:合成组曲/合成作曲家//0///"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &programSearchProbe{}
			worker := Worker{sources: curation.NewSources(nil)}
			got, err := worker.lookupHint(t.Context(), e, "album", tc.hint, snapshot, nil, probe, probe)
			if err != nil || !slices.Equal(probe.calls, tc.want) || got.Status == "matched" {
				t.Fatalf("实际查询=%v，期望 %v，状态=%s，错误=%v", probe.calls, tc.want, got.Status, err)
			}
		})
	}
}

type judgeRecordingProbe struct {
	b3MatchFinder
	query curation.RecordingSearch
	id    string
}

func (p *judgeRecordingProbe) SearchRecordingPage(_ context.Context, q curation.RecordingSearch) (curation.RecordingPage, error) {
	p.query = q
	return curation.RecordingPage{Items: []sources.Recording{{ID: p.id}}, PageInfo: curation.PageInfo{Complete: true}}, nil
}

func TestJudgeLooseTrackUsesRecordingAndDuration(t *testing.T) {
	for _, length := range []int64{100000, 140000} {
		t.Run(fmt.Sprint(length), func(t *testing.T) {
			e, release := b3Album(1)
			e.CandidateKind, e.Tracks[0].Artist = "loose_unknown", e.Artist
			rec := sources.Recording{ID: "synthetic-recording", Title: e.Tracks[0].Title, Artists: release.Artists, LengthMS: &length}
			probe := &judgeRecordingProbe{id: rec.ID, b3MatchFinder: b3MatchFinder{recordings: map[string]sources.Recording{rec.ID: rec}}}
			hint := curation.JudgeGroup{Name: curation.JudgeName{Track: rec.Title, Artist: e.Artist}}
			got, err := (&Worker{}).lookupHint(t.Context(), e, "single", hint, MatchSnapshot{}, nil, probe, probe)
			if err != nil || probe.query.Title != rec.Title || probe.query.Artist != e.Artist || probe.query.LengthMS == nil ||
				*probe.query.LengthMS != 100000 || !slices.Equal(probe.recordingReads, []string{rec.ID}) || len(probe.releaseReads) != 0 {
				t.Fatalf("散曲应以曲名、艺人、时长搜索并读取录音，实际=%+v，录音=%v，发行=%v，错误=%v", probe.query, probe.recordingReads, probe.releaseReads, err)
			}
			if length == 100000 {
				if got.Status != "matched" || got.Recording == nil || got.Recording.ID != rec.ID || !slices.Equal(got.Pairs, []TrackPair{{TrackID: e.Tracks[0].ID, RecordingID: rec.ID}}) {
					t.Fatalf("文本与时长吻合应对到录音：%+v", got)
				}
			} else if got.Status == "matched" || len(got.Pairs) != 0 {
				t.Fatalf("散曲的同名录音时长不符，应保留原样：%+v", got)
			}
		})
	}
}

func TestJudgeSameGroupEditionsContrastWithProgram(t *testing.T) {
	e, first := b3Album(4)
	first.Complete = true
	first.Media[0].TrackCount = len(e.Tracks)
	for i, track := range e.Tracks {
		first.Media[0].Tracks[i].Title = track.Title
	}
	second := first
	second.ID = "second-edition"
	probe := &programSearchProbe{releasesFor: func(curation.ReleaseSearch) []sources.Release {
		return []sources.Release{first, second}
	}}
	ordinary, err := decideSearchAlbum(t.Context(), e, albumPageResult(curation.ReleasePage{
		Items: []sources.Release{first, second}, PageInfo: curation.PageInfo{Complete: true},
	}, false), &searchDetails{finder: probe, attempted: map[string]bool{}})
	if err != nil || ordinary.Status != "matched" || ordinary.Level != "release_group" || ordinary.GroupID != first.Group.ID ||
		ordinary.Release != nil || len(ordinary.Pairs) != 0 || ordinary.Route != "" {
		t.Fatalf("S8 同组两版应直接认组：%+v，错误=%v", ordinary, err)
	}

	hint := curation.JudgeGroup{Name: curation.JudgeName{Release: first.Title}}
	judged, err := (&Worker{}).lookupHint(t.Context(), e, "album", hint, MatchSnapshot{}, nil, probe, probe)
	if err != nil {
		t.Fatal(err)
	}
	wantMatch(t, judged, "uncertain", "ambiguous_editions")
	if judged.Release != nil || judged.GroupID != "" || len(judged.Pairs) != 0 || judged.Route != "" {
		t.Fatalf("B2 同组歧义留给人选：%+v", judged)
	}
	var ids []string
	for _, candidate := range judged.Candidates {
		if candidate.Release == nil || candidate.Release.Group.ID != first.Group.ID || candidate.Verification == nil {
			t.Fatalf("人选候选应含同组版本和核对证据：%+v", candidate)
		}
		ids = append(ids, candidate.ExternalID)
	}
	if !slices.Equal(ids, []string{first.ID, second.ID}) {
		t.Fatalf("B2 应列出两个已核实的版，实际 %v", ids)
	}
}
