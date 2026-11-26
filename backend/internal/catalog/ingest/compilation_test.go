package ingest

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

func TestApplyCompilation(t *testing.T) {
	observations := func(albumArtist string, artists []string, compilations int) []SourceObservation {
		result := make([]SourceObservation, len(artists))
		for i, artist := range artists {
			result[i] = SourceObservation{SourceKind: "flac", Artist: artist, AlbumArtist: albumArtist, Compilation: i < compilations}
		}
		return result
	}
	for _, tc := range []struct {
		name                  string
		observations          []SourceObservation
		wantArtist, wantAlbum string
		wantCompilation       bool
	}{
		{"专辑艺人占位词", observations("Various Artists", []string{"甲", "乙"}, 0), "群星", "", true},
		{"全角专辑艺人占位词", observations("ＶＡ", []string{"甲", "乙"}, 0), "群星", "", true},
		{"全部曲目标为合辑", observations("", []string{"甲", "乙"}, 2), "群星", "", true},
		{"十轨仅三轨标为合辑", observations("", []string{"甲", "甲", "甲", "甲", "甲", "甲", "乙", "乙", "乙", "乙"}, 3), "甲", "甲", false},
		{"真实专辑艺人优先于合辑标志", observations("周杰伦", []string{"甲", "乙"}, 2), "周杰伦", "周杰伦", false},
		{"合辑标志为假不改变决定", observations("", []string{"甲", "甲", "乙"}, 0), "甲", "甲", false},
		{"二人合作专辑各半且有客串", observations("", []string{"A", "B", "A", "B", "A feat. C", "B feat. D"}, 0), "A / B", "A / B", false},
		{"曲目艺人多数为群星", observations("", []string{"群星", "群星", "甲"}, 0), "群星", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			artist := chooseReleaseArtist(tc.observations)
			albumArtist := chooseValue(tc.observations, "album_artist", func(o SourceObservation) string { return o.AlbumArtist })
			beforeArtist, beforeAlbum := artist, albumArtist
			applyCompilation(tc.observations, &artist, &albumArtist)
			if artist.Value != tc.wantArtist || albumArtist.Value != tc.wantAlbum {
				t.Fatalf("字段决定 = %q / %q，期望 %q / %q", artist.Value, albumArtist.Value, tc.wantArtist, tc.wantAlbum)
			}
			if !tc.wantCompilation {
				if albumArtist.Source == "artist_distribution" {
					if artist.Source != "artist_distribution" || !reflect.DeepEqual(artist.Candidates, beforeArtist.Candidates) {
						t.Fatal("缺少分布来源")
					}
					return
				}
				if !reflect.DeepEqual(artist, beforeArtist) || !reflect.DeepEqual(albumArtist, beforeAlbum) {
					t.Fatalf("非合辑的原决定被改写：%+v / %+v", artist, albumArtist)
				}
				return
			}
			want := FieldDecision{
				Field: "artist", Value: "群星", Source: "compilation", Confidence: "high",
				Action: "auto_apply", RuleID: "compilation_v1", Candidates: beforeArtist.Candidates,
			}
			if !reflect.DeepEqual(artist, want) {
				t.Fatalf("合辑决定 = %+v，期望 %+v", artist, want)
			}
			if !reflect.DeepEqual(albumArtist, FieldDecision{}) {
				t.Fatalf("合辑未清空专辑艺人决定：%+v", albumArtist)
			}
		})
	}
}

func TestOrganizeAlbumArtistVoteDistribution(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		artists                       []string
		explicit                      string
		compilation                   bool
		wantArtist, wantAlbum, source string
	}{
		{"三比二", []string{"A", "B", "A", "B", "A"}, "", false, "A", "A", "artist_distribution"},
		{"二人各半", []string{"B", "A"}, "", false, "A / B", "A / B", "artist_distribution"},
		{"三人并列", []string{"C", "B", "A"}, "", false, "A / B / C", "A / B / C", "artist_distribution"},
		{"四人并列", []string{"D", "C", "B", "A"}, "", false, "A / B / C / D", "A / B / C / D", "artist_distribution"},
		{"五人分散", []string{"E", "D", "C", "B", "A"}, "", false, "群星", "Various Artists", "artist_distribution"},
		{"五人中一人过半", []string{"A", "A", "A", "A", "A", "B", "C", "D", "E"}, "", false, "A", "A", "artist_distribution"},
		{"合辑标志", []string{"A", "B"}, "", true, "群星", "", "compilation"},
		{"显式专辑艺人优先", []string{"A", "B", "C", "D", "E"}, "Ensemble", true, "Ensemble", "Ensemble", "tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var observations []SourceObservation
			for i, artist := range tc.artists {
				observations = append(observations, SourceObservation{
					RelativePath: fmt.Sprintf("Mix/%02d.flac", i+1), TrackNumber: i + 1, Album: "Mix",
					Artist: artist + " feat. Guest", AlbumArtist: tc.explicit, Compilation: tc.compilation,
					ArtistTagEvidence: []metadata.ArtistTagEvidence{
						{Role: "track_artist", RawValue: "Guest", OccurrenceIndex: 1},
						{Role: "track_artist", RawValue: artist, OccurrenceIndex: 0},
					},
				})
			}
			c := organizeSingleCandidate(t, observations...)
			if c.Artist != tc.wantArtist || c.AlbumArtist != tc.wantAlbum {
				t.Errorf("发行艺人和专辑艺人 = %q / %q，期望 %q / %q", c.Artist, c.AlbumArtist, tc.wantArtist, tc.wantAlbum)
			}
			for _, decision := range c.Decisions {
				if (decision.Field == "artist" || decision.Field == "album_artist") && decision.Source != tc.source {
					t.Errorf("%s 来源 = %q，期望 %q", decision.Field, decision.Source, tc.source)
				}
			}
			for i, track := range c.Mediums[1] {
				if !reflect.DeepEqual(track.Observation.ArtistTagEvidence, observations[i].ArtistTagEvidence) || track.Observation.Artist != observations[i].Artist {
					t.Errorf("第 %d 轨原始署名被改写：%+v", i+1, track.Observation)
				}
			}
		})
	}
}
