package program

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type b3Prints func(string) FingerprintStatus

func (f b3Prints) Fingerprint(_ context.Context, id string) (TrackFingerprint, FingerprintStatus, error) {
	return TrackFingerprint{Value: id, DurationSeconds: 100}, f(id), nil
}

type b3FingerprintFinder func(string) ([]sources.FingerprintMatch, error)

func (f b3FingerprintFinder) LookupFingerprint(_ context.Context, fp sources.Fingerprint) ([]sources.FingerprintMatch, error) {
	return f(fp.Value)
}

type b3MatchFinder struct {
	sources.ReleaseFinder
	sources.RecordingFinder
	releases       map[string]sources.Release
	recordings     map[string]sources.Recording
	releaseReads   []string
	recordingReads []string
	search         []sources.Release
	searches       int
}

func (f *b3MatchFinder) ReleaseByID(_ context.Context, id string) (sources.Release, error) {
	f.releaseReads = append(f.releaseReads, id)
	return f.releases[id], nil
}

func (f *b3MatchFinder) ReleaseGroupDetails(_ context.Context, id string) (curation.ReleaseGroupDetails, error) {
	group := curation.ReleaseGroupDetails{ID: id, Complete: true}
	for _, r := range f.releases {
		if r.Group.ID == id {
			group.Title = r.Group.Title
			group.Editions = append(group.Editions, r)
		}
	}
	return group, nil
}

func (f *b3MatchFinder) RecordingByID(_ context.Context, id string) (sources.Recording, error) {
	f.recordingReads = append(f.recordingReads, id)
	return f.recordings[id], nil
}

func (f *b3MatchFinder) SearchReleases(_ context.Context, _ sources.ReleaseQuery) ([]sources.Release, error) {
	f.searches++
	return f.search, nil
}

func b3Album(n int) (catalog.CurationEvidence, sources.Release) {
	e, r := pairedAlbum(n)
	e.Barcodes, e.CatalogNumber = nil, ""
	return e, r
}

func TestB3FingerprintAlbumRules(t *testing.T) {
	for _, tc := range []struct {
		name, status, conflict, method string
		readIDs                        []string
	}{
		{"抽样不足", "uncertain", "list_truncated", "search", nil},
		{"暂时失败", "", "", "", nil},
		{"上游稍后再试", "", "", "", nil},
		{"只有一首命中", "uncertain", "acoustid_sparse", "acoustid", nil},
		{"交集为空", "uncertain", "acoustid_spread", "acoustid", nil},
		{"多于三个版本", "matched", "", "acoustid", []string{"fourth", "remote", "second", "third"}},
		{"过滤曲目数和碟数不符的版本", "matched", "", "acoustid", []string{"remote"}},
		{"配对录音不符", "uncertain", "acoustid_mismatch", "acoustid", []string{"remote"}},
		{"标题艺人不符", "uncertain", "title_artist_mismatch", "acoustid", []string{"remote"}},
		{"时长不符", "uncertain", "duration_mismatch", "acoustid", []string{"remote"}},
		{"两个版本都通过", "matched", "", "acoustid", []string{"remote", "second"}},
		{"全部通过", "matched", "", "acoustid", []string{"remote"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, r := b3Album(4)
			other := r
			other.ID = "second"
			f := &b3MatchFinder{releases: map[string]sources.Release{r.ID: r, other.ID: other}, search: []sources.Release{r}}
			if tc.name == "标题艺人不符" {
				e.Title, e.Artist = "另一张专辑", "另一支乐队"
			}
			if tc.name == "时长不符" {
				e.Tracks[0].DurationSeconds = 106
			}
			var lookups []string
			step := &FingerprintStep{
				Prints: b3Prints(func(id string) FingerprintStatus {
					if id != "local1" {
						switch tc.name {
						case "抽样不足":
							return FingerprintPermanent
						case "暂时失败":
							return FingerprintTransient
						}
					}
					return FingerprintOK
				}),
				Finder: b3FingerprintFinder(func(id string) ([]sources.FingerprintMatch, error) {
					lookups = append(lookups, id)
					rec := sources.FingerprintRecording{ID: "rec" + id[len("local"):], Releases: []sources.FingerprintRelease{
						{ID: "remote", TrackCount: 4, MediumCount: 1, Title: r.Title},
					}}
					switch tc.name {
					case "上游稍后再试":
						return nil, sources.ErrTryLater
					case "只有一首命中":
						if id != "local1" {
							return []sources.FingerprintMatch{{Score: 0.899, Recordings: []sources.FingerprintRecording{rec}}}, nil
						}
					case "交集为空":
						if id != "local1" {
							rec.Releases[0].ID = "second"
						}
					case "多于三个版本":
						rec.Releases = append(rec.Releases, sources.FingerprintRelease{ID: "second"}, sources.FingerprintRelease{ID: "third"}, sources.FingerprintRelease{ID: "fourth"})
					case "过滤曲目数和碟数不符的版本":
						rec.Releases = append(rec.Releases, sources.FingerprintRelease{ID: "wrong-tracks", TrackCount: 5, MediumCount: 1}, sources.FingerprintRelease{ID: "wrong-discs", TrackCount: 4, MediumCount: 2})
					case "配对录音不符":
						if id == "local1" {
							rec.ID = "wrong-recording"
						}
					case "两个版本都通过":
						rec.Releases = append(rec.Releases, sources.FingerprintRelease{ID: "second"})
					}
					return []sources.FingerprintMatch{{Score: 0.9, Recordings: []sources.FingerprintRecording{rec}}}, nil
				}),
			}
			got, err := MatchWithFingerprint(t.Context(), e, f, f, step)
			if tc.status == "" {
				if !errors.Is(err, sources.ErrTryLater) || got.Fingerprint != "used" || f.searches != 0 {
					t.Fatalf("结果=%+v 错误=%v 搜索=%d，期望稍后再试", got, err, f.searches)
				}
				if tc.name == "暂时失败" && len(lookups) != 0 {
					t.Fatalf("抽样不足仍查询=%v", lookups)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var conflicts []string
			if tc.conflict != "" {
				conflicts = append(conflicts, tc.conflict)
			}
			wantMatch(t, got, tc.status, conflicts...)
			if got.Fingerprint != "used" || got.Method != tc.method || !slices.Equal(f.releaseReads, tc.readIDs) {
				t.Fatalf("结果=%+v 读取=%v，期望依据=%s 读取=%v", got, f.releaseReads, tc.method, tc.readIDs)
			}
			if tc.method == "search" && (len(lookups) != 0 || f.searches != 1) {
				t.Fatalf("抽样不足时查指纹=%v 搜索=%d", lookups, f.searches)
			}
			if tc.name == "两个版本都通过" || tc.name == "多于三个版本" {
				if got.Level != "release_group" || got.GroupID != r.Group.ID || got.Release != nil || len(got.Pairs) != 0 {
					t.Fatalf("仅能确定同一发行组：%+v", got)
				}
			} else if tc.status == "matched" && (got.Release == nil || got.Release.ID != r.ID || !slices.Equal(got.Pairs, []TrackPair{{"local1", "rec1"}, {"local2", "rec2"}, {"local3", "rec3"}, {"local4", "rec4"}})) {
				t.Fatalf("发行及录音对应=%+v", got)
			}
		})
	}
}

func TestB3FingerprintSingleRules(t *testing.T) {
	for _, tc := range []struct {
		name, status, conflict string
		recordings, reads      int
	}{
		{"正式录音", "matched", "", 1, 1},
		{"多于三首", "uncertain", "too_many", 4, 0},
		{"多个候选", "uncertain", "ambiguous", 2, 2},
		{"时长不符", "uncertain", "duration_mismatch", 1, 0},
		{"标题不符", "uncertain", "title_mismatch", 1, 1},
		{"占位标题", "matched", "", 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := b3Album(1)
			e.CandidateKind = "loose_unknown"
			track := &e.Tracks[0]
			track.Artist = "正式艺人"
			track.TitlePlaceholder = tc.name == "占位标题"
			length, officialLength := int64(100000), int64(100000)
			if tc.name == "时长不符" {
				length = 106000
			}
			f := &b3MatchFinder{recordings: make(map[string]sources.Recording)}
			hit := sources.FingerprintMatch{Score: 0.97}
			for i := range tc.recordings {
				old, official := fmt.Sprintf("old-%d", i), fmt.Sprintf("official-%d", i)
				title := track.Title
				if tc.name == "标题不符" || tc.name == "占位标题" {
					title = "正式录音标题"
				}
				hit.Recordings = append(hit.Recordings, sources.FingerprintRecording{ID: old, Title: track.Title, LengthMS: &length})
				f.recordings[old] = sources.Recording{ID: official, Title: title, LengthMS: &officialLength, Artists: []sources.Credit{{Name: "正式艺人"}}}
			}
			step := &FingerprintStep{Prints: b3Prints(func(string) FingerprintStatus { return FingerprintOK }), Finder: b3FingerprintFinder(func(string) ([]sources.FingerprintMatch, error) {
				return []sources.FingerprintMatch{hit}, nil
			})}
			got, err := MatchWithFingerprint(t.Context(), e, f, f, step)
			if err != nil {
				t.Fatal(err)
			}
			var conflicts []string
			if tc.conflict != "" {
				conflicts = append(conflicts, tc.conflict)
			}
			if tc.name == "占位标题" {
				wantMatch(t, got, "no_match", "insufficient_evidence")
				if got.Method != "" || got.Fingerprint != "unavailable" || len(f.recordingReads) != 0 || len(got.Candidates) != 0 {
					t.Fatalf("标题占位且没有其它线索时不应提前查询声纹：%+v，读取=%v", got, f.recordingReads)
				}
				return
			}
			wantMatch(t, got, tc.status, conflicts...)
			var reads []string
			for i := range tc.reads {
				reads = append(reads, fmt.Sprintf("old-%d", i))
			}
			if got.Method != "acoustid" || got.Fingerprint != "used" || !slices.Equal(f.recordingReads, reads) || len(got.Candidates) != tc.recordings {
				t.Fatalf("结果=%+v，读回=%v，期望 %v", got, f.recordingReads, reads)
			}
			for _, candidate := range got.Candidates {
				if candidate.Score != hit.Score {
					t.Errorf("候选分数=%g，期望 %g", candidate.Score, hit.Score)
				}
			}
			if tc.status == "matched" && (got.Recording == nil || got.Recording.ID != "official-0" ||
				!slices.Equal(got.Pairs, []TrackPair{{track.ID, "official-0"}}) || got.Candidates[0].Artist != "正式艺人") {
				t.Fatalf("应使用 MusicBrainz 读回的正式数据：%+v", got)
			}
		})
	}
}

func TestB3FingerprintReachAndFallback(t *testing.T) {
	t.Run("信息不足", func(t *testing.T) {
		e, _ := b3Album(2)
		e.TitlePlaceholder, e.ArtistPlaceholder = true, true
		f := &b3MatchFinder{}
		for _, tc := range []struct {
			state string
			step  *FingerprintStep
		}{
			{"unavailable", nil},
			{"used", &FingerprintStep{Prints: b3Prints(func(string) FingerprintStatus { return FingerprintOK }), Finder: b3FingerprintFinder(func(string) ([]sources.FingerprintMatch, error) {
				return nil, sources.ErrInvalidQuery
			})}},
		} {
			got, err := MatchWithFingerprint(t.Context(), e, f, f, tc.step)
			if tc.step != nil {
				if !errors.Is(err, sources.ErrInvalidQuery) || got.Status != "" || f.searches != 0 {
					t.Fatalf("无效指纹转成了识别结论：结果=%+v 错误=%v 搜索=%d", got, err, f.searches)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.step == nil {
				wantMatch(t, got, "no_match", "insufficient_evidence")
			} else {
				wantMatch(t, got, "no_match")
			}
			if got.Fingerprint != tc.state || f.searches != 0 || len(got.Candidates) != 0 {
				t.Fatalf("结果=%+v 搜索=%d，指纹字段期望 %s", got, f.searches, tc.state)
			}
		}
	})
	for _, upstream := range []error{&sources.InvalidKeyError{Name: "user"}, operations.Conflict("source_disabled", "测试途中关闭指纹源")} {
		t.Run(upstream.Error(), func(t *testing.T) {
			e, r := b3Album(2)
			f := &b3MatchFinder{search: []sources.Release{r}}
			var calls int
			step := &FingerprintStep{
				Prints: b3Prints(func(string) FingerprintStatus { return FingerprintOK }),
				Finder: b3FingerprintFinder(func(string) ([]sources.FingerprintMatch, error) {
					calls++
					if calls == 1 {
						return nil, nil
					}
					return nil, upstream
				}),
			}
			got, err := MatchWithFingerprint(t.Context(), e, f, f, step)
			if err != nil {
				t.Fatal(err)
			}
			wantMatch(t, got, "uncertain", "list_truncated")
			if got.Fingerprint != "unavailable" || got.Method != "search" || calls != 2 || f.searches != 1 {
				t.Fatalf("结果=%+v 指纹查询=%d 搜索=%d", got, calls, f.searches)
			}
		})
	}
}

func TestB3MatchWithoutFingerprint(t *testing.T) {
	for _, name := range []string{"标识命中", "文字搜索", "搜索无结果", "信息不足", "单曲曲目数不符"} {
		t.Run(name, func(t *testing.T) {
			e, r := b3Album(2)
			f := &b3MatchFinder{releases: map[string]sources.Release{r.ID: r}}
			var want MatchResult
			switch name {
			case "标识命中":
				e.Tracks[0].Identifiers["mb_release"] = []string{r.ID}
				want = DecideAlbum(e, "tag_id", []sources.Release{r})
				want.Fingerprint = "not_reached"
			case "文字搜索":
				f.search = []sources.Release{r}
				want = uncertain(albumPageResult(curation.ReleasePage{Items: f.search}, false), "list_truncated")
				want.Fingerprint = "unavailable"
			case "搜索无结果":
				want = albumPageResult(curation.ReleasePage{}, false)
				want.Snapshot.Pages = append(want.Snapshot.Pages, SearchPage{Kind: "release", Relaxed: true})
				want.Snapshot.FormatRelaxed = true
				want.Route, want.Fingerprint = "lookup", "unavailable"
			case "信息不足":
				e.TitlePlaceholder, e.ArtistPlaceholder = true, true
				want = MatchResult{Mode: "album", Status: "no_match", Fingerprint: "unavailable", Conflicts: []string{"insufficient_evidence"}, Candidates: []Candidate{}}
			case "单曲曲目数不符":
				e.CandidateKind = "loose_unknown"
				want = MatchResult{Mode: "single", Status: "uncertain", Fingerprint: "not_reached", Conflicts: []string{"track_count"}, Candidates: []Candidate{}}
			}
			got, err := Match(t.Context(), e, f, f)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("结果=%+v 错误=%v，期望 %+v", got, err, want)
			}
			if name == "标识命中" {
				step := &FingerprintStep{Prints: b3Prints(func(string) FingerprintStatus {
					t.Fatal("标识已命中仍计算指纹")
					return FingerprintPermanent
				})}
				got, err := MatchWithFingerprint(t.Context(), e, f, f, step)
				if err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("传入指纹步骤后结果=%+v 错误=%v，期望 %+v", got, err, want)
				}
			}
		})
	}
}
