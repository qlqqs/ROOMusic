package program

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestMain(m *testing.M) {
	if err := textnorm.LoadEmbedded(); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}

func pairedAlbum(n int) (catalog.CurationEvidence, sources.Release) {
	e := catalog.CurationEvidence{ID: "local", Title: "合成专辑", Artist: "乐队", Media: []int{1}, Label: "唱片社", CatalogNumber: "CAT-1", Barcodes: []string{"123456789012"}}
	r := sources.Release{ID: "remote", Title: e.Title, Artists: []sources.Credit{{Name: e.Artist, ArtistID: "artist"}}, Labels: []sources.Label{{Name: e.Label, CatalogNumber: e.CatalogNumber}}, Barcode: "0123456789012", Group: sources.Group{ID: "group"}, Media: []sources.Medium{{Position: 1, Format: "CD", DiscIDs: []string{"disc"}}}}
	for i := 1; i <= n; i++ {
		id := fmt.Sprint(i)
		e.Tracks = append(e.Tracks, catalog.CurationTrack{ID: "local" + id, Title: "曲" + id, Disc: 1, Position: i, DurationSeconds: 100, Identifiers: map[string][]string{}})
		ms := int64(100000)
		r.Media[0].Tracks = append(r.Media[0].Tracks, sources.Track{ID: "track" + id, Position: i, RecordingID: "rec" + id, LengthMS: &ms, ISRCs: []string{"USABC2300001"}})
	}
	return e, r
}

func wantMatch(t *testing.T, got MatchResult, status string, conflicts ...string) {
	t.Helper()
	if got.Status != status || !slices.Equal(got.Conflicts, conflicts) {
		t.Fatalf("判定 %s %v，期望 %s %v", got.Status, got.Conflicts, status, conflicts)
	}
}

func TestAlbumContraryEvidence(t *testing.T) {
	for _, code := range []string{"duration_mismatch", "barcode_mismatch", "catalog_mismatch", "title_artist_mismatch", "release_tag_mismatch", "group_tag_mismatch", "artist_tag_mismatch", "disc_tag_mismatch", "recording_tag_mismatch", "track_tag_mismatch", "isrc_mismatch", "media_count_mismatch", "media_format_mismatch", "pseudo_release", "suppressed", "existing_link"} {
		t.Run(code, func(t *testing.T) {
			e, r := pairedAlbum(1)
			tag := func(kind string) { e.Tracks[0].Identifiers[kind] = []string{"different"} }
			method := "barcode"
			switch code {
			case "duration_mismatch":
				e.Tracks[0].DurationSeconds = 104
			case "barcode_mismatch":
				r.Barcode = "222222222222"
			case "catalog_mismatch":
				r.Labels[0].CatalogNumber = "CAT-2"
			case "title_artist_mismatch":
				r.Title = "另一张"
				r.Artists[0].Name = "另一人"
			case "release_tag_mismatch":
				tag("mb_release")
			case "group_tag_mismatch":
				tag("mb_release_group")
			case "artist_tag_mismatch":
				tag("mb_album_artist")
			case "disc_tag_mismatch":
				tag("mb_disc")
			case "recording_tag_mismatch":
				tag("mb_recording")
			case "track_tag_mismatch":
				tag("mb_track")
			case "isrc_mismatch":
				tag("isrc")
			case "media_count_mismatch":
				method = "tag_id"
				e.Media = append(e.Media, 2)
			case "media_format_mismatch":
				e.MediaType = "CD"
				r.Media[0].Format = "Vinyl"
			case "pseudo_release":
				r.Status = "Pseudo-Release"
			case "suppressed":
				e.Suppressions = []catalog.CurationSuppression{{TargetType: "release", TargetID: e.ID, Subject: "external_link", ValueHash: fmt.Sprintf("%x", sha256.Sum256([]byte("musicbrainz\x00"+r.ID)))}}
			case "existing_link":
				e.ExternalID = "old"
			}
			status := "uncertain"
			if code == "suppressed" || code == "existing_link" {
				status = "no_usable_candidates"
			}
			wantMatch(t, DecideAlbum(e, method, []sources.Release{r}), status, code)
		})
	}
}

func TestAlbumCorroboration(t *testing.T) {
	for _, method := range []string{"tag_id", "barcode", "catalog_number", "isrc", "disc_id", "search"} {
		t.Run(method, func(t *testing.T) {
			e, r := pairedAlbum(2)
			e.Tracks[0].CueISRC = "US-ABC-23-00001"
			got := DecideAlbum(e, method, []sources.Release{r})
			if method == "search" && (got.Level != "release" || got.Rule != "structure") {
				t.Fatalf("文字搜索结构吻合应定到版本：%+v", got)
			}
			wantMatch(t, got, "matched")
			if got.Release.ID != r.ID || !slices.Equal(got.Pairs, []TrackPair{{"local1", "rec1"}, {"local2", "rec2"}}) {
				t.Fatalf("对应错误 %+v", got)
			}
		})
	}
	t.Run("count_equal", func(t *testing.T) {
		e, r := pairedAlbum(2)
		e.Tracks = e.Tracks[:1]
		e.Tracks[0].DurationSeconds = 0
		wantMatch(t, DecideAlbum(e, "tag_id", []sources.Release{r}), "uncertain")
		wantMatch(t, DecideAlbum(e, "barcode", []sources.Release{r}), "uncertain", "track_count")
	})
	t.Run("durations_ok", func(t *testing.T) {
		for _, tc := range []struct {
			n, known int
			status   string
		}{{4, 2, "matched"}, {3, 1, "uncertain"}, {3, 2, "matched"}} {
			e, r := pairedAlbum(tc.n + 1)
			e.Tracks = e.Tracks[:tc.n]
			for i := tc.known; i < tc.n; i++ {
				e.Tracks[i].DurationSeconds = 0
			}
			wantMatch(t, DecideAlbum(e, "tag_id", []sources.Release{r}), tc.status)
		}
	})
	t.Run("barcode_second", func(t *testing.T) {
		e, r := pairedAlbum(1)
		e.Tracks[0].DurationSeconds = 0
		e.Title = "?"
		e.Artist = "!"
		r.Title = "!"
		r.Artists[0].Name = "?"
		wantMatch(t, DecideAlbum(e, "barcode", []sources.Release{r}), "uncertain")
	})
	t.Run("positions", func(t *testing.T) {
		e, r := pairedAlbum(3)
		e.Tracks = e.Tracks[1:]
		got := DecideAlbum(e, "tag_id", []sources.Release{r})
		wantMatch(t, got, "matched")
		if got.Pairs[0] != (TrackPair{"local2", "rec2"}) {
			t.Fatal(got.Pairs)
		}
		e.Tracks[0].PositionInferred = true
		wantMatch(t, DecideAlbum(e, "tag_id", []sources.Release{r}), "uncertain", "track_count")
		e, r = pairedAlbum(2)
		e.Tracks[1].PositionInferred = true
		wantMatch(t, DecideAlbum(e, "barcode", []sources.Release{r}), "matched")
		e.Tracks[1].DurationSeconds = 0
		wantMatch(t, DecideAlbum(e, "barcode", []sources.Release{r}), "uncertain", "inferred_positions")
		e.Tracks[1].Position = 1
		wantMatch(t, DecideAlbum(e, "tag_id", []sources.Release{r}), "uncertain", "position_conflict")
	})
	t.Run("candidate_limits", func(t *testing.T) {
		e, r := pairedAlbum(1)
		rows := []sources.Release{}
		for i := 0; i < 4; i++ {
			r.ID = fmt.Sprint(i)
			rows = append(rows, r)
		}
		got := DecideAlbum(e, "barcode", rows[:3])
		wantMatch(t, got, "matched")
		if got.Level != "release_group" || got.GroupID != r.Group.ID || got.Release != nil || len(got.Pairs) != 0 {
			t.Fatalf("同组的三个版本只应定到发行组：%+v", got)
		}
		got = DecideAlbum(e, "barcode", rows)
		wantMatch(t, got, "matched")
		if got.Level != "release_group" {
			t.Fatal(got.Level)
		}
		e.Tracks[0].Identifiers["mb_release"] = []string{"1", "2"}
		wantMatch(t, DecideAlbum(e, "tag_id", nil), "uncertain", "mixed_release_tags")
	})
}

func TestSingleRules(t *testing.T) {
	e, _ := pairedAlbum(1)
	tr := &e.Tracks[0]
	ms := int64(100000)
	r := sources.Recording{ID: "rec", LengthMS: &ms, Title: tr.Title, ISRCs: []string{"USABC2300001"}}
	decide := func(method string) MatchResult { return DecideSingle(e, method, []sources.Recording{r}) }
	for _, method := range []string{"tag_id", "isrc"} {
		got := decide(method)
		wantMatch(t, got, "matched")
		if got.Recording.ID != "rec" || got.Pairs[0].TrackID != tr.ID {
			t.Fatal(got)
		}
	}
	wantMatch(t, decide("search"), "uncertain", "artist_mismatch")
	r.Title = "其他"
	wantMatch(t, decide("isrc"), "uncertain")
	tr.CueISRC = "USABC2300002"
	wantMatch(t, decide("tag_id"), "uncertain", "isrc_mismatch")
	tr.CueISRC = ""
	tr.DurationSeconds = 103
	wantMatch(t, decide("tag_id"), "matched")
	tr.DurationSeconds = 104
	wantMatch(t, decide("tag_id"), "uncertain", "duration_mismatch")
	ms = 500000
	tr.DurationSeconds = 505
	wantMatch(t, decide("tag_id"), "matched")
	tr.DurationSeconds = 506
	wantMatch(t, decide("tag_id"), "uncertain", "duration_mismatch")
	r.LengthMS = nil
	wantMatch(t, decide("tag_id"), "uncertain")
}

func TestLookupBounds(t *testing.T) {
	e, r := pairedAlbum(1)
	rows := make([]sources.Release, 25)
	for i := range rows {
		rows[i] = sources.Release{ID: fmt.Sprint(i)}
	}
	four := slices.Clone(rows[:4])
	for i := range four {
		four[i] = r
		four[i].ID = rows[i].ID
		four[i].Complete = true
		four[i].Status = "Pseudo-Release"
	}
	for _, tc := range []struct {
		name, method, code string
		list               releaseList
	}{
		{"barcode", "barcode", "list_truncated", releasePages([][]sources.Release{rows})},
		{"catalog", "catalog_number", "list_truncated", releasePages([][]sources.Release{rows})},
		{"isrc", "isrc", "list_truncated", isrcReleaseList([][]sources.Recording{{{Releases: rows}}})},
		{"spread", "isrc", "isrc_spread", isrcReleaseList([][]sources.Recording{{{Releases: rows[:1]}}, {{Releases: rows[1:2]}}})},
		{"fuzzy", "disc_id", "disc_fuzzy", discReleaseList([]sources.DiscMatch{{Exact: false, Releases: []sources.Release{r}}})},
		{"four", "barcode", "pseudo_release", releasePages([][]sources.Release{four})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fullCandidates(t.Context(), nil, e, tc.method, tc.list)
			if err != nil {
				t.Fatal(err)
			}
			wantMatch(t, got, "uncertain", tc.code)
			if len(got.Candidates) == 0 || len(got.Candidates) != len(tc.list.items) {
				t.Fatal(got.Candidates)
			}
		})
	}
	for _, mode := range []string{"ordinary_directory", "loose_unknown"} {
		empty := catalog.CurationEvidence{CandidateKind: mode, TitlePlaceholder: true, ArtistPlaceholder: true, Tracks: []catalog.CurationTrack{{TitlePlaceholder: true, ArtistPlaceholder: true}}}
		got, err := Match(t.Context(), empty, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		wantMatch(t, got, "no_match", "insufficient_evidence")
	}
}

type priorityFinder struct {
	sources.ReleaseFinder
	release sources.Release
	calls   []string
}

func (f *priorityFinder) ReleaseByID(_ context.Context, id string) (sources.Release, error) {
	f.calls = append(f.calls, "id:"+id)
	return f.release, nil
}
func (f *priorityFinder) ReleasesByBarcode(context.Context, string) ([]sources.Release, error) {
	f.calls = append(f.calls, "barcode")
	return []sources.Release{f.release}, nil
}

func TestFirstHitStopsLookup(t *testing.T) {
	e, r := pairedAlbum(1)
	e.Tracks[0].Identifiers["mb_release"] = []string{r.ID}
	for _, status := range []string{"matched", "uncertain"} {
		t.Run(status, func(t *testing.T) {
			f := &priorityFinder{release: r}
			if status == "uncertain" {
				f.release.Status = "Pseudo-Release"
			}
			got, err := Match(t.Context(), e, f, nil)
			if err != nil || got.Method != "tag_id" || got.Status != status || !slices.Equal(f.calls, []string{"id:" + r.ID}) {
				t.Fatalf("%+v %v 调用 %v", got, err, f.calls)
			}
		})
	}
}

func TestAlbumMethodRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, method, status string
		edit                 func(*catalog.CurationEvidence, *sources.Release)
	}{
		{"barcode_title_only", "barcode", "matched", func(e *catalog.CurationEvidence, r *sources.Release) {
			e.Tracks[0].DurationSeconds = 0
			e.ArtistPlaceholder = true
		}},
		{"barcode_artist_only", "barcode", "matched", func(e *catalog.CurationEvidence, r *sources.Release) {
			e.Tracks[0].DurationSeconds = 0
			e.TitlePlaceholder = true
		}},
		{"catalog_label_only", "catalog_number", "matched", func(e *catalog.CurationEvidence, r *sources.Release) { e.Tracks[0].DurationSeconds = 0 }},
		{"catalog_duration_only", "catalog_number", "matched", func(e *catalog.CurationEvidence, r *sources.Release) { e.Label = "" }},
		{"catalog_neither", "catalog_number", "uncertain", func(e *catalog.CurationEvidence, r *sources.Release) {
			e.Tracks[0].DurationSeconds = 0
			e.Label = ""
		}},
		{"isrc_unconfirmed", "isrc", "uncertain", func(e *catalog.CurationEvidence, r *sources.Release) {
			e.Tracks[0].CueISRC = "USABC2300001"
			r.Media[0].Tracks[0].ISRCs = nil
		}},
		{"disc_without_title_or_artist", "disc_id", "uncertain", func(e *catalog.CurationEvidence, r *sources.Release) {
			e.TitlePlaceholder = true
			e.ArtistPlaceholder = true
		}},
		{"tag_count_only", "tag_id", "matched", func(e *catalog.CurationEvidence, r *sources.Release) { e.Tracks[0].DurationSeconds = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, r := pairedAlbum(1)
			tc.edit(&e, &r)
			wantMatch(t, DecideAlbum(e, tc.method, []sources.Release{r}), tc.status)
		})
	}
}
