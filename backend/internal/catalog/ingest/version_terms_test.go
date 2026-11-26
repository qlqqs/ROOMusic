package ingest

import (
	"slices"
	"testing"
)

func TestOrganizeVersionTermsPreserveBracketedTitles(t *testing.T) {
	const dir = "Album (Deluxe) [2009] {EP} <单曲>"
	const title = "Album (deluxe) (Outer [Remastered 2009]) (Single) (OST) (SDTK) (Soundtrack) (原声) (原声带) (海边现场)"
	o := SourceObservation{RelativePath: dir + "/01.flac", Album: title, Edition: "Platinum", TrackNumber: 1}
	c := organizeSingleCandidate(t, o)
	if want := []string{"Deluxe", "Remastered 2009", "海边现场"}; !slices.Equal(c.VersionTerms, want) || c.Title != title || c.Edition != "Platinum" {
		t.Fatalf("版本词=%v，标题=%q，版次=%q；期望版本词 %v 和原显示字段", c.VersionTerms, c.Title, c.Edition, want)
	}
	for _, suffix := range []string{" (Deluxe)", " (海边现场)"} {
		for _, position := range []int{2, 4} {
			other := SourceObservation{RelativePath: dir + "/Second.flac", Album: title + suffix, TrackNumber: position}
			got := OrganizeObservations([]SourceObservation{o, other})
			want := 1
			if position == 4 {
				want = 2
			}
			if len(got) != want {
				t.Errorf("标题 %q / %q，曲号 1,%d 得到 %d 张，期望 %d 张", o.Album, other.Album, position, len(got), want)
			}
			if position == 4 && len(got) == 2 {
				titles := []string{got[0].Title, got[1].Title}
				if !slices.Contains(titles, o.Album) || !slices.Contains(titles, other.Album) {
					t.Errorf("按名拆分后括号文字丢失：%v", titles)
				}
			}
		}
	}
}
