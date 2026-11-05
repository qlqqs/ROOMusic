package metadata

import (
	"reflect"
	"testing"
)

func TestSplitArtistNamesFlattensConservativeSeparators(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{name: "spaced slash", input: "Artist Beta / Artist Alpha", want: []string{"Artist Beta", "Artist Alpha"}},
		{name: "nested comma and ampersand", input: "Artist Alpha, Artist Beta & Artist Gamma", want: []string{"Artist Alpha", "Artist Beta", "Artist Gamma"}},
		{name: "compact slash", input: "Artist Alpha/Artist Beta", want: []string{"Artist Alpha", "Artist Beta"}},
		{name: "fixed ampersand group", input: "Simon & Garfunkel", want: []string{"Simon & Garfunkel"}},
		{name: "fixed slash group", input: "AC/DC", want: []string{"AC/DC"}},
		{name: "fixed comma group", input: "Earth, Wind & Fire", want: []string{"Earth, Wind & Fire"}},
		{name: "control cleanup before fixed group", input: "AC/\x00DC", want: []string{"AC/DC"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitArtistNames(tt.input); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("艺人拆分结果 = %#v，期望 %#v", got, tt.want)
			}
		})
	}
}

func TestCanonicalArtistNamesPreserveSpellingsAndDeduplicateExactNames(t *testing.T) {
	got := CanonicalArtistNames("Jay Chou / 周杰倫 / 周杰伦 / jay chou / Jay Chou / Guest Artist")
	want := []string{"Jay Chou", "周杰倫", "周杰伦", "jay chou", "Guest Artist"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("艺人原文去重结果 = %#v，期望 %#v", got, want)
	}
}

func TestCanonicalTrackArtistUsesStableDisplayOrder(t *testing.T) {
	if got := CanonicalTrackArtist("Artist Beta, Artist Alpha"); got != "Artist Alpha / Artist Beta" {
		t.Fatalf("Track artist = %q，期望稳定排序后的展示值", got)
	}
}
