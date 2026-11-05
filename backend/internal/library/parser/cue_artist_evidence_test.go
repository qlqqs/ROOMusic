package parser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCueArtistEvidencePreservesSheetSelectionAndTrackValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "album.cue")
	content := `PERFORMER " First Album "
PERFORMER "Last Album"
FILE "image.wav" WAVE
 TRACK 01 AUDIO
  PERFORMER " Jay Chou "
  PERFORMER "周杰倫"
  PERFORMER "Jay Chou"
  INDEX 01 00:00:00
 TRACK 02 AUDIO
  INDEX 01 00:01:00
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := ParseCueDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if document.Artist != "Last Album" || document.Tracks[0].Artist != "Jay Chou / 周杰倫" || document.Tracks[1].Artist != "First Album / Last Album" {
		t.Fatalf("CUE选择或多值集合改变：%+v", document)
	}
	if len(document.ArtistTagEvidence) != 2 || document.ArtistTagEvidence[0].RawValue != " First Album " || document.ArtistTagEvidence[1].OccurrenceIndex != 1 {
		t.Fatalf("sheet原文丢失：%+v", document.ArtistTagEvidence)
	}
	first := document.Tracks[0].ArtistTagEvidence
	if len(first) != 3 || first[0].RawValue != " Jay Chou " || first[2].OccurrenceIndex != 2 || first[0].SourceKind != "cue_track" {
		t.Fatalf("track原文丢失：%+v", first)
	}
	if len(document.Tracks[1].ArtistTagEvidence) != 0 {
		t.Fatal("前轨证据污染后轨")
	}
}

func TestCueArtistEvidenceRejectsOversizedAndCombinedValues(t *testing.T) {
	for _, tc := range []struct {
		name               string
		sheet, track, size int
		valid              bool
	}{
		{"轨上限", 0, 100, 4, true}, {"轨溢出", 0, 101, 4, false},
		{"sheet加track溢出", 50, 51, 4, false}, {"原文上限", 0, 1, 4096, true}, {"原文溢出", 0, 1, 4097, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cue strings.Builder
			value := strings.Repeat("x", tc.size)
			for i := 0; i < tc.sheet; i++ {
				fmt.Fprintf(&cue, "PERFORMER %q\n", value)
			}
			cue.WriteString("FILE \"image.wav\" WAVE\n TRACK 01 AUDIO\n")
			for i := 0; i < tc.track; i++ {
				fmt.Fprintf(&cue, "PERFORMER %q\n", value)
			}
			cue.WriteString(" INDEX 01 00:00:00\n")
			path := filepath.Join(t.TempDir(), "album.cue")
			if err := os.WriteFile(path, []byte(cue.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := ParseCueDocument(path)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t，err=%v", tc.valid, err)
			}
		})
	}
}
