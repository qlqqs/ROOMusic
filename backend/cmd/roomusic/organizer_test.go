package main

import "testing"

func TestOrganizeLooseAndUnknown(t *testing.T) {
	got := organizeObservations([]sourceObservation{{RelativePath: "a.flac", Album: "Album", TrackNumber: 1}, {RelativePath: "b.flac", Album: "Album", TrackNumber: 2}, {RelativePath: "c.flac", Title: "x"}})
	if len(got) != 2 {
		t.Fatalf("candidates=%d", len(got))
	}
	if got[0].Anchor.Kind != candidateLooseAlbum && got[1].Anchor.Kind != candidateLooseAlbum {
		t.Fatalf("missing loose album candidate: %#v", got)
	}
}

func TestOrganizeMajorityDeterministic(t *testing.T) {
	a := []sourceObservation{{RelativePath: "z.flac", Album: "Same", AlbumArtist: "B"}, {RelativePath: "a.flac", Album: "Same", AlbumArtist: "A"}, {RelativePath: "m.flac", Album: "Other", AlbumArtist: "A"}}
	b := []sourceObservation{a[2], a[0], a[1]}
	x, y := organizeObservations(a), organizeObservations(b)
	if len(x) != len(y) || x[0].Title != y[0].Title || x[0].Artist != y[0].Artist {
		t.Fatalf("order dependent: %#v %#v", x, y)
	}
}

func TestOrganizeStrictMultidisc(t *testing.T) {
	obs := []sourceObservation{{RelativePath: "Disc 1/a.flac", Directory: "", Album: "Box", DiscNumber: 1, TrackNumber: 1}, {RelativePath: "Disc 2/b.flac", Album: "Box", DiscNumber: 2, TrackNumber: 1}}
	got := organizeObservations(obs)
	if len(got) != 1 {
		t.Fatalf("got %d candidates", len(got))
	}
	if got[0].Anchor.Kind != candidateMultiDisc {
		t.Fatalf("kind=%s", got[0].Anchor.Kind)
	}
	if len(got[0].Mediums) != 2 {
		t.Fatalf("mediums=%d", len(got[0].Mediums))
	}
}
