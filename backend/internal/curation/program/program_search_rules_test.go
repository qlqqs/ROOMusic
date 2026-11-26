package program

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
)

type programSearchProbe struct {
	b3MatchFinder
	calls         []string
	lookup        func(string) sources.Release
	discs         map[string]sources.DiscMatch
	releasesFor   func(curation.ReleaseSearch) []sources.Release
	recordingsFor func(curation.RecordingSearch) []sources.Recording
	artists       []sources.Artist
}

func (f *programSearchProbe) ReleaseByID(_ context.Context, id string) (sources.Release, error) {
	f.calls = append(f.calls, "release:"+id)
	if f.lookup != nil {
		return f.lookup(id), nil
	}
	return f.releases[id], nil
}

func (f *programSearchProbe) ReleasesByDisc(_ context.Context, toc sources.DiscTOC) (sources.DiscMatch, error) {
	id, err := musicbrainz.DiscID(toc)
	f.calls = append(f.calls, "disc:"+id)
	return f.discs[id], err
}

func (f *programSearchProbe) SearchReleasePage(_ context.Context, q curation.ReleaseSearch) (curation.ReleasePage, error) {
	f.calls = append(f.calls, fmt.Sprintf("search:%s/%s/%s/%d/%s/%s/%s", q.Title, q.Artist, q.ArtistID,
		q.TrackCount, strings.Join(q.Barcodes, ","), q.CatalogNumber, q.Label))
	var rows []sources.Release
	if f.releasesFor != nil {
		rows = f.releasesFor(q)
	}
	return curation.ReleasePage{Items: rows, PageInfo: curation.PageInfo{Complete: true}}, nil
}

func (f *programSearchProbe) SearchRecordingPage(_ context.Context, q curation.RecordingSearch) (curation.RecordingPage, error) {
	f.calls = append(f.calls, "isrc:"+strings.Join(q.ISRCs, ","))
	return curation.RecordingPage{Items: f.recordingsFor(q), PageInfo: curation.PageInfo{Complete: true}}, nil
}

func (f *programSearchProbe) SearchArtistPage(_ context.Context, name string) (curation.ArtistPage, error) {
	f.calls = append(f.calls, "artist:"+name)
	return curation.ArtistPage{Items: f.artists, PageInfo: curation.PageInfo{Complete: true}}, nil
}

func (f *programSearchProbe) ArtistByID(_ context.Context, id string) (sources.Artist, error) {
	f.calls = append(f.calls, "artist-id:"+id)
	return sources.Artist{}, nil
}

func (f *programSearchProbe) SearchArtists(_ context.Context, name string) ([]sources.Artist, error) {
	f.calls = append(f.calls, "simple-artist:"+name)
	return f.artists, nil
}

func TestProgramIdentifierSearchOrder(t *testing.T) {
	for _, tc := range []struct {
		name, method, conflict string
		stages                 int
	}{
		{"tag", "tag_id", "", 1}, {"tag-counterevidence", "tag_id", "duration_mismatch", 1},
		{"cue-intersection", "disc_id", "", 3}, {"cue-disjoint", "disc_id", "disc_spread", 3},
		{"barcode", "barcode", "", 4}, {"barcode-counterevidence", "barcode", "barcode_mismatch", 4},
		{"catalog-and-label", "catalog_number", "", 5}, {"catalog-label-counterevidence", "catalog_number", "catalog_mismatch", 5},
		{"isrc", "isrc", "", 6}, {"isrc-disjoint", "isrc", "isrc_spread", 6},
		{"fingerprint", "acoustid", "", 11}, {"fingerprint-counterevidence", "acoustid", "acoustid_mismatch", 11},
		{"all-identifiers-empty", "", "", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, r := pairedAlbum(8)
			e.Tracks[0].Identifiers["mb_release"] = []string{r.ID}
			e.Media = []int{1, 2}
			r.Complete = true
			r.Media = []sources.Medium{
				{Position: 1, Format: "CD", Tracks: r.Media[0].Tracks[:4]},
				{Position: 2, Format: "CD", Tracks: r.Media[0].Tracks[4:]},
			}
			for i := range e.Tracks {
				disc, position := i/4+1, i%4+1
				index := (position - 1) * 7500
				end := disc*75 + 30000
				tr := &e.Tracks[i]
				tr.Disc, tr.Position, tr.SourceKind = disc, position, "cue_virtual"
				tr.CueSheetPath, tr.ParentFile = fmt.Sprintf("CD%d.cue", disc), fmt.Sprintf("CD%d.flac", disc)
				tr.CueIndexFrames, tr.CueEndFrames = &index, &end
				isrc := fmt.Sprintf("USABC230000%d", i+1)
				tr.Identifiers["isrc"] = []string{isrc}
				remoteTrack := &r.Media[disc-1].Tracks[position-1]
				remoteTrack.Position, remoteTrack.ISRCs = position, []string{isrc}
			}
			tocs := DiscTOCs(e)
			if len(tocs) != 2 ||
				!slices.Equal(tocs[0].Offsets, []int{150, 7650, 15150, 22650}) {
				t.Fatalf("两张 CUE 碟的 TOC = %+v", tocs)
			}
			var discIDs []string
			for i, toc := range tocs {
				id, err := musicbrainz.DiscID(toc)
				if err != nil {
					t.Fatal(err)
				}
				discIDs = append(discIDs, id)
				r.Media[i].DiscIDs = []string{id}
			}
			if discIDs[0] == discIDs[1] {
				t.Fatalf("两张碟的编号相同：%v", discIDs)
			}
			switch tc.conflict {
			case "duration_mismatch":
				ms := int64(150000)
				r.Media[0].Tracks[0].LengthMS = &ms
			case "barcode_mismatch":
				r.Barcode = "4006381333931"
			case "catalog_mismatch":
				r.Labels = []sources.Label{{Name: "另一厂牌", CatalogNumber: e.CatalogNumber}, {Name: e.Label, CatalogNumber: "OTHER"}}
			}
			f := &programSearchProbe{discs: map[string]sources.DiscMatch{}}
			f.lookup = func(id string) sources.Release {
				if id == r.ID && (tc.method == "tag_id" || tc.method == "acoustid" && len(f.calls) > 1) {
					return r
				}
				return sources.Release{}
			}
			if tc.method == "disc_id" {
				left, right := sources.Release{ID: "left-only"}, sources.Release{ID: "right-only"}
				f.discs[discIDs[0]] = sources.DiscMatch{Exact: true, Releases: []sources.Release{left, r}}
				f.discs[discIDs[1]] = sources.DiscMatch{Exact: true, Releases: []sources.Release{right, r}}
				if tc.conflict == "disc_spread" {
					f.discs[discIDs[1]] = sources.DiscMatch{Exact: true, Releases: []sources.Release{right}}
				}
			}
			f.releasesFor = func(q curation.ReleaseSearch) []sources.Release {
				if tc.method == "barcode" && len(q.Barcodes) > 0 || tc.method == "catalog_number" && q.CatalogNumber != "" {
					return []sources.Release{r}
				}
				return nil
			}
			f.recordingsFor = func(q curation.RecordingSearch) []sources.Recording {
				if tc.method != "isrc" {
					return nil
				}
				var rows []sources.Recording
				for _, m := range r.Media {
					for _, tr := range m.Tracks {
						if !slices.Contains(q.ISRCs, tr.ISRCs[0]) {
							continue
						}
						release := r
						if tc.conflict == "isrc_spread" && len(rows) > 0 {
							release.ID = "unrelated"
						}
						rows = append(rows, sources.Recording{ID: tr.RecordingID, ISRCs: tr.ISRCs, Releases: []sources.Release{release}})
					}
				}
				return rows
			}
			fp := &FingerprintStep{Prints: b3Prints(func(string) FingerprintStatus { return FingerprintOK }),
				Finder: b3FingerprintFinder(func(id string) ([]sources.FingerprintMatch, error) {
					f.calls = append(f.calls, "fingerprint:"+id)
					if tc.method != "acoustid" {
						return nil, nil
					}
					recording := "rec" + strings.TrimPrefix(id, "local")
					if tc.conflict == "acoustid_mismatch" && id == "local1" {
						recording = "unrelated"
					}
					return []sources.FingerprintMatch{{Score: 0.99, Recordings: []sources.FingerprintRecording{
						{ID: recording, ReleaseIDs: []string{r.ID}},
					}}}, nil
				})}
			result, err := matchWithOptions(t.Context(), e, f, f, fp, MatchOptions{Artists: f})
			want := []string{"release:remote", "disc:" + discIDs[0], "disc:" + discIDs[1],
				fmt.Sprintf("search:///0/%s//", strings.Join(e.SearchBarcodes(), ",")), "search:///0//CAT-1/唱片社",
				"isrc:USABC2300001,USABC2300003,USABC2300006,USABC2300008",
				"fingerprint:local1", "fingerprint:local3", "fingerprint:local6", "fingerprint:local8", "release:remote"}
			if err != nil || !slices.Equal(f.calls, want[:tc.stages]) {
				t.Fatalf("查找顺序 %v，期望 %v，错误 %v", f.calls, want[:tc.stages], err)
			}
			if result.Method != tc.method {
				t.Fatalf("查找方法 %q，期望 %q", result.Method, tc.method)
			}
			switch {
			case tc.conflict != "":
				if result.Status != "uncertain" || !slices.Contains(result.Conflicts, tc.conflict) || result.Release != nil {
					t.Fatalf("应保留 %q 反证并停止查找：%+v", tc.conflict, result)
				}
			case tc.method != "":
				if result.Status != "matched" || result.Release == nil || result.Release.ID != r.ID {
					t.Fatalf("应命中 %s：%+v", r.ID, result)
				}
			default:
				wantMatch(t, result, "no_match")
			}
		})
	}
}

func TestProgramNameSearchOrder(t *testing.T) {
	for _, tc := range []struct {
		name, hit string
		artists   []sources.Artist
		requests  int
	}{
		{"strict", "strict", nil, 1},
		{"relaxed", "relaxed", nil, 2},
		{"no-artist", "", nil, 3},
		{"ambiguous-artist", "", []sources.Artist{{ID: "one", Name: "乐队"}, {ID: "two", Name: "乐队"}}, 3},
		{"artist-id", "arid", []sources.Artist{{ID: "artist", Name: "外文名", Aliases: []sources.Alias{{Name: "乐队"}}}}, 4},
		{"all-empty", "", []sources.Artist{{ID: "artist", Name: "乐队"}}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, r := b3Album(5)
			e.Title += "（豪华版） [320k]"
			e.ArtistMBID, r.Complete = "old-name-credit", true
			if tc.hit == "arid" {
				r.Title = "异写标题"
			}
			f := &programSearchProbe{artists: tc.artists}
			f.releasesFor = func(q curation.ReleaseSearch) []sources.Release {
				if tc.hit == "strict" && q.Title == e.Title || tc.hit == "relaxed" && q.Title == "合成专辑" ||
					tc.hit == "arid" && q.Title == "" && q.Artist == "" && q.ArtistID == "artist" {
					return []sources.Release{r}
				}
				return nil
			}
			got, err := matchWithOptions(t.Context(), e, f, f, nil, MatchOptions{Artists: f})
			want := []string{"search:" + e.Title + "/乐队//5///", "search:合成专辑/乐队//0///",
				"artist:乐队", "search://artist/5///"}
			if err != nil || !slices.Equal(f.calls, want[:tc.requests]) {
				t.Fatalf("名字查询 %v，期望 %v，错误 %v", f.calls, want[:tc.requests], err)
			}
			if tc.hit == "" {
				wantMatch(t, got, "no_match")
			} else if got.Status != "matched" || got.Release == nil || got.Release.ID != r.ID {
				t.Fatalf("名字查询应对上发行 %s：%+v", r.ID, got)
			}
		})
	}
}

type simpleNameFinder struct {
	b3MatchFinder
	queries []sources.ReleaseQuery
}

func (f *simpleNameFinder) SearchReleases(_ context.Context, q sources.ReleaseQuery) ([]sources.Release, error) {
	f.queries = append(f.queries, q)
	return f.search, nil
}

func TestProgramSimpleFinderBoundary(t *testing.T) {
	e, r := b3Album(5)
	e.Title += " deluxe [FLAC]"
	artist := &programSearchProbe{artists: []sources.Artist{{ID: "artist", Name: e.Artist}}}
	f := &simpleNameFinder{}
	got, err := matchWithOptions(t.Context(), e, f, f, nil, MatchOptions{Artists: artist})
	want := []sources.ReleaseQuery{{Title: e.Title, Artist: e.Artist, TrackCount: 5}, {Title: r.Title, Artist: e.Artist}}
	if err != nil || !reflect.DeepEqual(f.queries, want) || len(artist.calls) != 0 {
		t.Fatalf("简单 finder 查询 %+v，期望 %+v，艺人查询 %v，错误 %v", f.queries, want, artist.calls, err)
	}
	wantMatch(t, got, "no_match")

	f.queries, f.search = nil, []sources.Release{r}
	got, err = matchWithOptions(t.Context(), e, f, f, nil, MatchOptions{Artists: artist})
	if err != nil || !reflect.DeepEqual(f.queries, want[:1]) || len(artist.calls) != 0 || len(f.releaseReads) != 0 {
		t.Fatalf("简单 finder 非空页应停在首查：查询 %v，艺人 %v，详情 %v，错误 %v", f.queries, artist.calls, f.releaseReads, err)
	}
	wantMatch(t, got, "uncertain", "list_truncated")
}

func TestProgramReleaseAdmission(t *testing.T) {
	e, r := b3Album(5)
	for _, tc := range []struct {
		name                     string
		fingerprint, allowed, id bool
		change                   func(*catalog.CurationEvidence)
	}{
		{"both-names", false, true, false, nil},
		{"title-only", true, false, false, func(e *catalog.CurationEvidence) { e.ArtistPlaceholder = true }},
		{"artist-only", false, false, false, func(e *catalog.CurationEvidence) { e.TitlePlaceholder = true }},
		{"title-only-with-id", false, true, true, func(e *catalog.CurationEvidence) {
			e.ArtistPlaceholder = true
			e.Tracks[0].Identifiers["mb_release"] = []string{r.ID}
		}},
		{"placeholders-without-fingerprint", false, false, false, func(e *catalog.CurationEvidence) {
			e.TitlePlaceholder, e.ArtistPlaceholder = true, true
		}},
		{"placeholders-with-fingerprint", true, true, false, func(e *catalog.CurationEvidence) {
			e.TitlePlaceholder, e.ArtistPlaceholder = true, true
		}},
		{"assisted-title", false, true, false, func(e *catalog.CurationEvidence) {
			e.TitlePlaceholder, e.LocalTitle = true, r.Title
		}},
		{"loose-uses-track-names", false, true, false, func(e *catalog.CurationEvidence) {
			e.CandidateKind, e.TitlePlaceholder, e.ArtistPlaceholder = "loose_unknown", true, true
			e.Tracks = e.Tracks[:1]
			e.Tracks[0].Artist = "散曲艺人"
		}},
		{"compilation", false, true, false, func(e *catalog.CurationEvidence) {
			e.ArtistPlaceholder = true
			e.FieldSources["artist"] = "compilation"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := e
			ev.Tracks = slices.Clone(e.Tracks)
			ev.Tracks[0].Identifiers = map[string][]string{}
			ev.Authenticity = map[string]metadata.Authenticity{"title": metadata.Real, "artist": metadata.Real}
			ev.FieldSources = map[string]string{}
			if tc.change != nil {
				tc.change(&ev)
			}
			got, id := releaseAdmission(ev, tc.fingerprint)
			if got != tc.allowed || id != tc.id {
				t.Fatalf("准入、编号 = %t、%t，期望 %t、%t", got, id, tc.allowed, tc.id)
			}
			if tc.name == "compilation" {
				f := &programSearchProbe{}
				if _, err := matchWithOptions(t.Context(), ev, f, f, nil, MatchOptions{}); err != nil ||
					len(f.calls) == 0 || !strings.Contains(f.calls[0], "/"+variousArtistsID+"/") {
					t.Fatalf("合辑应使用群星编号：请求 %v，错误 %v", f.calls, err)
				}
			}
		})
	}
}
