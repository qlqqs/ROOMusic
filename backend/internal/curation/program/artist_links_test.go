package program

import (
	"slices"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func TestAlignCredits(t *testing.T) {
	local := []catalog.CurationArtistCredit{{Position: 1, Name: "Alpha", ArtistID: "a"}, {Position: 2, Name: "Beta", ArtistID: "b"}}
	alpha := sources.Credit{Name: "ALPHA", ArtistID: "x"}
	beta := sources.Credit{Name: "Beta", ArtistID: "y"}
	alias := sources.Credit{Name: "别名", ArtistID: "y"}
	want := []curation.LinkIntent{{ObjectType: "artist", ObjectID: "a", ExternalID: "x"}, {ObjectType: "artist", ObjectID: "b", ExternalID: "y"}}
	for _, tc := range []struct {
		name     string
		external []sources.Credit
		tags     []string
		want     []curation.LinkIntent
	}{
		{"按位松比较", []sources.Credit{alpha, beta}, nil, want},
		{"数量不等", []sources.Credit{alpha}, nil, nil},
		{"异名同标签", []sources.Credit{alpha, alias}, []string{"x", "y"}, want},
		{"异名异标签整组丢弃", []sources.Credit{alpha, alias}, []string{"x", "z"}, nil},
		{"标签数量不等", []sources.Credit{alpha, alias}, []string{"y"}, nil},
		{"仅有标签没有外部署名", nil, []string{"x", "y"}, nil},
		{"位置颠倒", []sources.Credit{beta, alpha}, nil, nil},
		{"Various Artists", []sources.Credit{alpha, {Name: "Beta", ArtistID: "89ad4ac3-39f7-470e-963a-56509c546377"}}, nil, nil},
		{"[unknown]", []sources.Credit{alpha, {Name: "Beta", ArtistID: "125ec42a-7229-4250-afc5-e057484327fe"}}, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := alignCredits(local, tc.external, tc.tags); !slices.Equal(got, tc.want) {
				t.Fatalf("对应 %+v，期望 %+v", got, tc.want)
			}
		})
	}
}

func TestAlignCreditsConflictingArtist(t *testing.T) {
	a := []catalog.CurationArtistCredit{{Position: 1, Name: "甲", ArtistID: "a"}}
	b := []catalog.CurationArtistCredit{{Position: 1, Name: "乙", ArtistID: "b"}}
	local := catalog.CurationArtistCredits{ReleaseArtists: b, Tracks: map[string][]catalog.CurationArtistCredit{"t1": a, "t2": a}}
	e := catalog.CurationEvidence{Tracks: []catalog.CurationTrack{{ID: "t1"}, {ID: "t2"}}}
	match := MatchResult{Release: &sources.Release{Artists: []sources.Credit{{Name: "乙", ArtistID: "y"}}, Media: []sources.Medium{{Tracks: []sources.Track{
		{RecordingID: "r1", Artists: []sources.Credit{{Name: "甲", ArtistID: "x"}}},
		{RecordingID: "r2", Artists: []sources.Credit{{Name: "甲", ArtistID: "z"}}},
	}}}}, Pairs: []TrackPair{{"t1", "r1"}, {"t2", "r2"}}}
	want := []curation.LinkIntent{{ObjectType: "artist", ObjectID: "b", ExternalID: "y"}}
	if got := planArtistLinks(local, e, match); !slices.Equal(got, want) {
		t.Fatalf("同人对应两个编号时应只保留乙，得到 %+v", got)
	}
}

func TestAlignCreditsTrackLayers(t *testing.T) {
	for _, tc := range []struct {
		name, eligible, effective, remote string
		write                             bool
	}{
		{"自动名字相同仍跳过", "扫描署名", "自动署名", "自动署名", false},
		{"自动名字不同仍按扫描署名写", "扫描署名", "自动署名", "扫描署名", true},
		{"人工修正替代扫描", "修正署名", "修正署名", "修正署名", true},
		{"人工修正后扫描不能佐证", "修正署名", "修正署名", "扫描署名", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// catalog 提供取证署名，曲目的扫描值和用于展示的有效值也会随证据传入。
			local := catalog.CurationArtistCredits{Tracks: map[string][]catalog.CurationArtistCredit{
				"track": {{Position: 1, Name: tc.eligible, ArtistID: "local"}},
			}}
			evidence := catalog.CurationEvidence{Tracks: []catalog.CurationTrack{{ID: "track", Artist: "扫描署名", EffectiveArtist: tc.effective}}}
			match := MatchResult{Recording: &sources.Recording{ID: "recording", Artists: []sources.Credit{{Name: tc.remote, ArtistID: "external"}}}, Pairs: []TrackPair{{"track", "recording"}}}
			var want []curation.LinkIntent
			if tc.write {
				want = []curation.LinkIntent{{ObjectType: "artist", ObjectID: "local", ExternalID: "external"}}
			}
			if got := planArtistLinks(local, evidence, match); !slices.Equal(got, want) {
				t.Fatalf("取证署名 %q、有效署名 %q，得到 %+v，期望 %+v", tc.eligible, tc.effective, got, want)
			}
		})
	}
}
