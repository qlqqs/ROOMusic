package program

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestProgramAlbumGroupSummary(t *testing.T) {
	for _, tc := range []struct {
		name, status, level, rule string
		versions, tracks, reads   int
	}{
		{"one", "matched", "release", "structure", 1, 5, 1},
		{"two", "matched", "release_group", "unique_group", 2, 5, 0},
		{"three", "matched", "release_group", "unique_group", 3, 5, 0},
		{"four", "matched", "release_group", "unique_group", 4, 5, 0},
		{"many", "matched", "release_group", "unique_group", 7, 5, 0},
		{"inferred-title", "matched", "release_group", "unique_group", 4, 5, 1},
		{"different-groups", "uncertain", "", "", 4, 5, 0},
		{"compilation", "matched", "release_group", "structure", 4, 5, 4},
		{"classical", "matched", "release_group", "structure", 4, 5, 4},
		{"four-tracks", "matched", "release_group", "structure", 4, 4, 4},
		{"truncated", "uncertain", "", "", 4, 5, 0},
		{"all-blocked", "no_usable_candidates", "", "", 4, 5, 0},
		{"partly-blocked", "matched", "release_group", "unique_group", 4, 5, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, base := b3Album(tc.tracks)
			e.Compilation, e.Classical = tc.name == "compilation", tc.name == "classical"
			if e.Compilation {
				base.Artists[0].ArtistID = variousArtistsID
			}
			e.Tracks[0].TitleInferred = tc.name == "inferred-title"
			base.Media[0].Tracks[0].Title = "外部首曲"
			base.Group.FirstReleaseDate = "1998-04-03"
			f := &b3MatchFinder{releases: map[string]sources.Release{}}
			count, offset := tc.versions, 0
			page := curation.ReleasePage{PageInfo: curation.PageInfo{Count: &count, Offset: &offset, Limit: 100, Returned: tc.versions, Complete: tc.name != "truncated"}}
			if tc.name == "truncated" {
				count = 101
			}
			for i := range tc.versions {
				r := base
				r.ID, r.Date, r.Complete = fmt.Sprintf("remote-%d", i), fmt.Sprint(2020-i), true
				if tc.name == "different-groups" && i == 0 {
					r.Group.ID = "other-group"
				}
				f.releases[r.ID] = r
				r.Complete, r.Media = false, []sources.Medium{{Position: 1, TrackCount: tc.tracks}}
				page.Items = append(page.Items, r)
				if tc.name == "all-blocked" || tc.name == "partly-blocked" && i == 0 {
					e.Suppressions = append(e.Suppressions, catalog.CurationSuppression{TargetType: "release", TargetID: e.ID,
						Subject: "external_link", ValueHash: fmt.Sprintf("%x", sha256.Sum256([]byte("musicbrainz\x00"+r.ID)))})
				}
			}
			got, err := decideSearchAlbum(t.Context(), e, albumPageResult(page, false), &searchDetails{finder: f, attempted: map[string]bool{}})
			if err != nil || got.Status != tc.status || got.Level != tc.level || got.Rule != tc.rule || len(f.releaseReads) != tc.reads {
				t.Fatalf("结论 %s/%s/%s，详情 %v，冲突 %v，错误 %v，期望 %s/%s/%s，详情 %d 次",
					got.Status, got.Level, got.Rule, f.releaseReads, got.Conflicts, err, tc.status, tc.level, tc.rule, tc.reads)
			}
			if tc.level == "release_group" && (got.GroupID != base.Group.ID || got.GroupFirstReleaseDate != "1998-04-03" ||
				got.Release != nil || len(got.Pairs) != 0 || got.Route != "") {
				t.Fatalf("组级结果含版级对应、录音或待定路线，或缺少组首发：%+v", got)
			}
			if tc.name == "inferred-title" && (!slices.Equal(f.releaseReads, []string{"remote-3"}) ||
				got.RepresentativeID != "remote-3" || got.TrackTitles[e.Tracks[0].ID] != "外部首曲") {
				t.Fatalf("推断曲名应只读最早版本：%v，代表版 %q，曲名 %v", f.releaseReads, got.RepresentativeID, got.TrackTitles)
			}
		})
	}
}

func TestProgramPartialAlbumNamesOnlineAndReplay(t *testing.T) {
	for _, shape := range []struct {
		name, rule    string
		local, remote int
	}{
		{"missing-track", "partial_subset", 4, 5},
		{"bonus-tail", "partial_superset", 5, 4},
		{"short-ep", "partial_superset", 4, 2},
	} {
		for _, names := range []string{"same-key", "different-title", "different-artist"} {
			t.Run(shape.name+"/"+names, func(t *testing.T) {
				e, r := b3Album(max(shape.local, shape.remote))
				e.Tracks, r.Media[0].Tracks = e.Tracks[:shape.local], r.Media[0].Tracks[:shape.remote]
				r.Complete = true
				for i := range r.Media[0].Tracks {
					r.Media[0].Tracks[i].Title = fmt.Sprintf("曲%d", i+1)
				}
				switch names {
				case "same-key":
					e.Title, r.Title = "Signal–Archive", "Signal Archive"
				case "different-title":
					r.Title = "另一张专辑"
				case "different-artist":
					r.Artists[0].Name = "另一支乐队"
				}
				f := &programSearchProbe{releasesFor: func(curation.ReleaseSearch) []sources.Release { return []sources.Release{r} }}
				online, err := Match(t.Context(), e, f, f)
				if err != nil {
					t.Fatal(err)
				}
				offline := replayMatch(e, online.Candidates, online.Conflicts, online.Snapshot)
				if names == "same-key" {
					for _, got := range []MatchResult{online, offline} {
						if got.Status != "matched" || got.Level != "release_group" || got.GroupID != r.Group.ID ||
							got.Rule != shape.rule || got.Release != nil || len(got.Pairs) > 0 {
							t.Fatalf("同键部分吻合应认到组，结果 %s/%s/%s，冲突 %v", got.Status, got.Level, got.Rule, got.Conflicts)
						}
					}
				} else {
					for _, got := range []MatchResult{online, offline} {
						if got.Status != "uncertain" || got.GroupID != "" || got.Release != nil || len(got.Pairs) > 0 {
							t.Fatalf("异名部分吻合应保留内部核对证据，结果 %s/%s/%s，冲突 %v", got.Status, got.Level, got.Rule, got.Conflicts)
						}
					}
				}
			})
		}
	}
}

func TestProgramGroupYearField(t *testing.T) {
	e, _ := b3Album(5)
	fresh := groupYearField(e, "1998-04-03")
	if len(fresh) != 1 || fresh["year"].Value != 1998 {
		t.Fatalf("组首发年份自动写入 = %+v", fresh)
	}
	raw, err := json.Marshal(fresh["year"])
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, date string
		change     func(*catalog.CurationEvidence)
	}{
		{"no-date", "", nil}, {"invalid-date", "19xx", nil},
		{"existing-year", "1998", func(e *catalog.CurationEvidence) { e.Year = 2017 }},
		{"manual-year", "1998", func(e *catalog.CurationEvidence) { e.Corrected = map[string]bool{"year": true} }},
		{"automatic-year", "1998", func(e *catalog.CurationEvidence) { e.Automatic = map[string]bool{"year": true} }},
		{"suppressed-year", "1998", func(e *catalog.CurationEvidence) {
			e.Suppressions = []catalog.CurationSuppression{{TargetType: "release", TargetID: e.ID, Subject: "year",
				ValueHash: fmt.Sprintf("%x", sha256.Sum256(raw))}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := e
			if tc.change != nil {
				tc.change(&ev)
			}
			if got := groupYearField(ev, tc.date); len(got) != 0 {
				t.Fatalf("意外的年份写入 %+v", got)
			}
		})
	}
}
