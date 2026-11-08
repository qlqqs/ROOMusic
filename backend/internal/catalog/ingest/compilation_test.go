package ingest

import (
	"reflect"
	"testing"
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
		{"十轨仅三轨标为合辑", observations("", []string{"甲", "甲", "甲", "甲", "甲", "甲", "乙", "乙", "乙", "乙"}, 3), "甲", "", false},
		{"真实专辑艺人优先于合辑标志", observations("周杰伦", []string{"甲", "乙"}, 2), "周杰伦", "周杰伦", false},
		{"合辑标志为假不改变决定", observations("", []string{"甲", "甲", "乙"}, 0), "甲", "", false},
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
