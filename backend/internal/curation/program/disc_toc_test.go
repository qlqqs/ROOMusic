package program_test

import (
	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation/program"
	"github.com/qlqq/roomusic/backend/internal/sources/musicbrainz"
	"testing"
)

func TestDiscTOCFromCue(t *testing.T) {
	indexes := []int{0, 22567, 41687, 58123, 71903, 91177, 104451, 115191, 131981, 143735, 159673, 174401}
	for _, name := range []string{"known_vector", "missing_index", "missing_end", "two_cues", "two_parents", "gap", "unordered", "other_disc_physical"} {
		t.Run(name, func(t *testing.T) {
			e := catalog.CurationEvidence{}
			for i := range indexes {
				e.Tracks = append(e.Tracks, catalog.CurationTrack{Disc: 1, Position: i + 1, SourceKind: "cue_virtual", CueSheetPath: "disc.cue", ParentFile: "disc.flac", CueIndexFrames: &indexes[i]})
			}
			end := 266975
			e.Tracks[len(indexes)-1].CueEndFrames = &end
			switch name {
			case "missing_index":
				e.Tracks[1].CueIndexFrames = nil
			case "missing_end":
				e.Tracks[len(indexes)-1].CueEndFrames = nil
			case "two_cues":
				e.Tracks[1].CueSheetPath = "two.cue"
			case "two_parents":
				e.Tracks[1].ParentFile = "two.flac"
			case "gap":
				e.Tracks[1].Position = 30
			case "unordered":
				e.Tracks[1].CueIndexFrames = &indexes[0]
			case "other_disc_physical":
				e.Tracks = append(e.Tracks, catalog.CurationTrack{Disc: 2, Position: 1, SourceKind: "physical"})
			}
			tocs := program.DiscTOCs(e)
			if name != "known_vector" {
				if len(tocs) != 0 {
					t.Fatal(tocs)
				}
				return
			}
			if len(tocs) != 1 {
				t.Fatal(tocs)
			}
			id, err := musicbrainz.DiscID(tocs[0])
			if err != nil || id != "bDCCdX8vZbnGiGD4tcN8aGNdkZc-" {
				t.Fatalf("目录 %+v 得到 %q %v", tocs, id, err)
			}
		})
	}
}
