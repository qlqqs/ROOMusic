package metadata

import (
	"slices"
	"testing"
)

func TestScanReadNamesAndFallback(t *testing.T) {
	for _, sep := range []string{";", "；", " / ", " feat. ", " ft. ", " with ", "、", "\r\n", "\x00"} {
		t.Run("separator_"+sep, func(t *testing.T) {
			if got := CanonicalArtistNames("甲" + sep + "乙"); !slices.Equal(got, []string{"甲", "乙"}) {
				t.Errorf("分隔符 %q 得到 %q，期望甲、乙", sep, got)
			}
		})
	}
	for _, raw := range []string{"AC/DC", "甲,乙", "First One & Second Two", "Bach, Johann Sebastian", "甲（乙、丙;丁 / 戊）", "甲(乙 with 丙)"} {
		t.Run(raw, func(t *testing.T) {
			if got := CanonicalArtistNames(raw); !slices.Equal(got, []string{raw}) {
				t.Errorf("姓名 %q 得到 %q，期望保留整串", raw, got)
			}
		})
	}
	for genre, want := range map[string]bool{
		"古典": true, "古典音乐": true, "古典乐": true, "Classical": true,
		"新古典": false, "古典跨界": false, "印度古典": false, "Classical Crossover": false,
	} {
		t.Run(genre, func(t *testing.T) {
			if got := HasClassicalGenre([]string{"Rock", genre}); got != want {
				t.Errorf("流派 %q 判为古典 = %t，期望 %t", genre, got, want)
			}
		})
	}
}
