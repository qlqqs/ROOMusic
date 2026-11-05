package metadata

import (
	"strings"
	"testing"
)

func TestArtistTagEvidenceValidatesRawBudget(t *testing.T) {
	for _, tc := range []struct {
		name        string
		count, size int
		valid       bool
	}{
		{"无证据", 0, 0, true}, {"最大项目", 100, 4096, true}, {"超出项目", 101, 1, false}, {"超出原文", 1, 4097, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := make([]ArtistTagEvidence, tc.count)
			for i := range values {
				values[i] = ArtistTagEvidence{Role: "track_artist", TagKey: "ARTIST", RawValue: strings.Repeat("x", tc.size), SourceKind: "flac_vorbis_comment", OccurrenceIndex: i}
			}
			if err := ValidateArtistTagEvidence(values); (err == nil) != tc.valid {
				t.Fatalf("valid=%t，err=%v", tc.valid, err)
			}
		})
	}
}

func TestCanonicalArtistNamesPreservesWholeMultiValueDisplay(t *testing.T) {
	left, right := strings.Repeat("a", 4096), strings.Repeat("b", 4096)
	if got := CanonicalArtistNames(left + " / " + right); len(got) != 2 || got[0] != left || got[1] != right {
		t.Fatal("有界多值展示被整串截断")
	}
}
