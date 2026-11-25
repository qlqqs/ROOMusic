package program

import (
	"context"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func Test发行组唯一的条件与结构判定(t *testing.T) {
	for _, tc := range []struct {
		name, status, level, rule, conflict string
		tracks, reads                       int
		change                              func(*catalog.CurationEvidence, *curation.ReleasePage, *b3MatchFinder)
	}{
		{"真实完整同组", "matched", "release_group", "unique_group", "", 5, 0, nil},
		{"推断标题只走结构", "matched", "release", "structure", "", 5, 1, func(e *catalog.CurationEvidence, _ *curation.ReleasePage, _ *b3MatchFinder) {
			e.Authenticity["title"] = metadata.Inferred
		}},
		{"推断艺人只走结构", "matched", "release", "structure", "", 5, 1, func(e *catalog.CurationEvidence, _ *curation.ReleasePage, _ *b3MatchFinder) {
			e.Authenticity["artist"] = metadata.Inferred
		}},
		{"第一页截断", "uncertain", "", "", "list_truncated", 5, 0, func(_ *catalog.CurationEvidence, p *curation.ReleasePage, _ *b3MatchFinder) {
			*p.Count = 101
			p.Complete = false
		}},
		{"合辑只走结构", "matched", "release", "structure", "", 5, 1, func(e *catalog.CurationEvidence, _ *curation.ReleasePage, _ *b3MatchFinder) {
			e.Compilation = true
		}},
		{"古典只走结构", "matched", "release", "structure", "", 5, 1, func(e *catalog.CurationEvidence, _ *curation.ReleasePage, _ *b3MatchFinder) {
			e.Classical = true
		}},
		{"四首只走结构", "matched", "release", "structure", "", 4, 1, nil},
		{"同曲数不同分碟", "uncertain", "", "", "track_count", 5, 0, func(_ *catalog.CurationEvidence, p *curation.ReleasePage, _ *b3MatchFinder) {
			left, right := 2, 3
			p.Counts[p.Items[0].ID] = curation.ReleaseCounts{Total: p.Counts[p.Items[0].ID].Total, Media: []*int{&left, &right}}
		}},
		{"结构吻合但跨组", "uncertain", "", "", "ambiguous", 5, 2, func(_ *catalog.CurationEvidence, p *curation.ReleasePage, f *b3MatchFinder) {
			addAlbumCandidate(p, f, "另一组")
		}},
		{"同组多版都通过结构", "matched", "release_group", "structure", "", 4, 2, func(_ *catalog.CurationEvidence, p *curation.ReleasePage, f *b3MatchFinder) {
			addAlbumCandidate(p, f, p.Items[0].Group.ID)
		}},
		{"短合辑时长互为反证", "uncertain", "", "", "duration_mismatch", 4, 1, func(e *catalog.CurationEvidence, _ *curation.ReleasePage, _ *b3MatchFinder) {
			e.Compilation = true
			e.Tracks[0].DurationSeconds += 10
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, r := b3Album(tc.tracks)
			e.Authenticity = map[string]metadata.Authenticity{"title": metadata.Real, "artist": metadata.Real}
			r.Complete = true
			f := &b3MatchFinder{releases: map[string]sources.Release{r.ID: r}}
			summary := r
			summary.Complete = false
			summary.Media = []sources.Medium{{Position: 1, TrackCount: tc.tracks}}
			count, offset, tracks := 1, 0, tc.tracks
			p := curation.ReleasePage{
				Items: []sources.Release{summary}, Counts: map[string]curation.ReleaseCounts{r.ID: {Total: &tracks, Media: []*int{&tracks}}},
				PageInfo: curation.PageInfo{Count: &count, Offset: &offset, Limit: 100, Returned: 1, Complete: true},
			}
			if tc.change != nil {
				tc.change(&e, &p, f)
			}
			got, err := decideSearchAlbum(t.Context(), e, albumPageResult(p, false), f, false)
			if err != nil || got.Status != tc.status || got.Level != tc.level || got.Rule != tc.rule || len(f.releaseReads) != tc.reads ||
				tc.conflict != "" && !slices.Contains(got.Conflicts, tc.conflict) {
				t.Fatalf("判定 %+v，详情读取 %v，错误 %v", got, f.releaseReads, err)
			}
			if got.Level == "release_group" && (got.GroupID != r.Group.ID || got.Release != nil || len(got.Pairs) != 0) {
				t.Fatalf("组级结论携带了版本或录音对应：%+v", got)
			}
		})
	}
}

func addAlbumCandidate(p *curation.ReleasePage, f *b3MatchFinder, group string) {
	summary := p.Items[0]
	detail := f.releases[summary.ID]
	p.Counts["second"] = p.Counts[summary.ID]
	summary.ID, summary.Group.ID = "second", group
	detail.ID, detail.Group.ID = summary.ID, group
	p.Items = append(p.Items, summary)
	f.releases[detail.ID] = detail
	*p.Count, p.Returned = len(p.Items), len(p.Items)
}

func Test标签发行编号不能覆盖文字反证(t *testing.T) {
	e, r := b3Album(5)
	e.Authenticity = map[string]metadata.Authenticity{"title": metadata.Real, "artist": metadata.Real}
	e.Title, e.Artist = "同结构的另一张专辑", "另一位艺人"
	e.Tracks[0].Identifiers["mb_release"] = []string{r.ID}
	for i := range e.Tracks {
		e.Tracks[i].Authenticity = map[string]metadata.Authenticity{"title": metadata.Real}
		r.Media[0].Tracks[i].Title = "无关曲目"
	}
	f := &b3MatchFinder{releases: map[string]sources.Release{r.ID: r}}
	got, err := Match(t.Context(), e, f, f)
	wantMatch(t, got, "uncertain", "tag_text_mismatch")
	if err != nil || got.Release != nil || len(got.Pairs) != 0 || !slices.Equal(f.releaseReads, []string{r.ID}) || f.searches != 0 {
		t.Fatalf("错误标签编号的结果 %+v，读取 %v，搜索 %d，错误 %v", got, f.releaseReads, f.searches, err)
	}
}

type singlePageFinder struct {
	sources.RecordingFinder
	rows []sources.Recording
}

func (f singlePageFinder) SearchRecordingPage(_ context.Context, _ curation.RecordingSearch) (curation.RecordingPage, error) {
	n, offset := len(f.rows), 0
	return curation.RecordingPage{PageInfo: curation.PageInfo{Count: &n, Offset: &offset, Limit: 100, Returned: n, Complete: true}, Items: f.rows}, nil
}

func Test散放单曲的录音和共同艺人(t *testing.T) {
	e, _ := b3Album(1)
	e.CandidateKind = "loose_unknown"
	tr := &e.Tracks[0]
	tr.Artist = e.Artist
	tr.Authenticity = map[string]metadata.Authenticity{"title": metadata.Real, "artist": metadata.Real}
	ms := int64(100000)
	r := sources.Recording{ID: "录音一", Title: tr.Title, LengthMS: &ms, Artists: []sources.Credit{{Name: tr.Artist, ArtistID: "同一身份"}}}
	other := r
	other.ID = "录音二"
	homonym := other
	homonym.Artists = []sources.Credit{{Name: tr.Artist, ArtistID: "同名不同身份"}}
	for _, tc := range []struct {
		name, status, level string
		rows                []sources.Recording
	}{
		{"唯一录音", "matched", "recording", []sources.Recording{r}},
		{"多个录音的共同署名", "matched", "artist", []sources.Recording{r, other}},
		{"同名不同艺人编号", "uncertain", "", []sources.Recording{r, homonym}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Match(t.Context(), e, nil, singlePageFinder{rows: tc.rows})
			if err != nil || got.Status != tc.status || got.Level != tc.level || got.Release != nil {
				t.Fatalf("单曲判定 %+v，错误 %v", got, err)
			}
			switch tc.level {
			case "recording":
				if got.Recording == nil || got.Recording.ID != r.ID || !slices.Equal(got.Pairs, []TrackPair{{tr.ID, r.ID}}) {
					t.Fatalf("唯一录音结果 %+v", got)
				}
			case "artist":
				if !slices.Equal(got.Credits, r.Artists) || got.Recording != nil || len(got.Pairs) != 0 {
					t.Fatalf("共同艺人结果 %+v", got)
				}
			default:
				wantMatch(t, got, "uncertain", "ambiguous")
			}
		})
	}
}
