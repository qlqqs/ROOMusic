package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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
	obs := []sourceObservation{{RelativePath: "Disc 1/a.flac", Directory: "", Album: "Box", DiscNumber: 1, TrackNumber: 1}, {RelativePath: "CD2/b.flac", Album: "Box", DiscNumber: 2, TrackNumber: 1}}
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

func TestOrganizeStrictMultidiscUsesPhysicalDiscFolderEvidence(t *testing.T) {
	parent := "Artist - Album (2024) [CAT-1001]"
	got := organizeObservations([]sourceObservation{
		{RelativePath: parent + "/CD1/01.flac", Directory: parent + "/CD1", Album: "Album", Artist: "Artist", DiscNumber: 1, TrackNumber: 1},
		{RelativePath: parent + "/CD2/01.flac", Directory: parent + "/CD2", Album: "Album", Artist: "Artist", DiscNumber: 2, TrackNumber: 1},
	})
	if len(got) != 1 || got[0].Anchor.Kind != candidateMultiDisc {
		t.Fatalf("unexpected strict multi-disc candidate: %#v", got)
	}
	if got[0].CatalogNumber != "" {
		t.Fatalf("parent grouping folder invented catalog metadata: %#v", got[0])
	}
	for _, decision := range got[0].Decisions {
		if decision.Field == "catalog_number" {
			t.Fatalf("parent grouping folder invented catalog evidence: %#v", decision)
		}
	}
}

func TestOrganizeStrictMultidiscWithoutAlbumTagUsesParentFolder(t *testing.T) {
	got := organizeObservations([]sourceObservation{
		{RelativePath: "Box/Disc 1/a.flac", Directory: "Box/Disc 1", Album: "Disc 1", InferredFields: map[string]bool{"album": true, "disc_number": true}, DiscNumber: 1},
		{RelativePath: "Box/CD2/b.flac", Directory: "Box/CD2", Album: "CD2", InferredFields: map[string]bool{"album": true, "disc_number": true}, DiscNumber: 1},
	})
	if len(got) != 1 || got[0].Anchor.Kind != candidateMultiDisc || got[0].Title != "Box" {
		t.Fatalf("strict multi-disc fallback = %#v", got)
	}
	if got[0].Decisions[0].Source != "folder_fallback" || got[0].Decisions[0].Confidence != "low" {
		t.Fatalf("unexpected multi-disc fallback provenance: %#v", got[0].Decisions[0])
	}
	if len(got[0].Mediums) != 2 {
		t.Fatalf("inferred disc numbers did not use folder structure: %#v", got[0].Mediums)
	}
}

func TestOrganizeSameDirectoryConflictUsesMajorityOrStableSplit(t *testing.T) {
	majority := organizeObservations([]sourceObservation{
		{RelativePath: "album/01.flac", Directory: "album", Album: "Winner", AlbumArtist: "Artist"},
		{RelativePath: "album/02.flac", Directory: "album", Album: "Winner", AlbumArtist: "Artist"},
		{RelativePath: "album/03.flac", Directory: "album", Album: "Other", AlbumArtist: "Artist"},
	})
	if len(majority) != 1 || majority[0].Title != "Winner" {
		t.Fatalf("strict majority was not retained with attention: %#v", majority)
	}
	foundFieldAttention := false
	for _, decision := range majority[0].Decisions {
		if decision.Field == "title" && decision.Action == "uncertain_apply" {
			foundFieldAttention = true
		}
	}
	if !foundFieldAttention || len(majority[0].Attention) != 0 {
		t.Fatalf("field attention was missing or duplicated as grouping attention: %#v", majority[0])
	}

	tie := []sourceObservation{
		{RelativePath: "album/02.flac", Directory: "album", Album: "Beta", AlbumArtist: "B"},
		{RelativePath: "album/01.flac", Directory: "album", Album: "Alpha", AlbumArtist: "A"},
	}
	got := organizeObservations(tie)
	reversed := organizeObservations([]sourceObservation{tie[1], tie[0]})
	if len(got) != 2 || !reflect.DeepEqual(got, reversed) {
		t.Fatalf("tie did not produce stable split: %#v %#v", got, reversed)
	}
	for _, candidate := range got {
		if candidate.Anchor.Kind != candidateSameDirSplit || candidate.Anchor.Partition == "" {
			t.Fatalf("unexpected split candidate: %#v", candidate.Anchor)
		}
		if !reflect.DeepEqual(candidate.Attention, []string{"same_directory_conflict"}) {
			t.Fatalf("split grouping attention = %#v", candidate.Attention)
		}
	}
}

func TestOrganizeNonContiguousDiscDirectoriesStayLeafCandidates(t *testing.T) {
	got := organizeObservations([]sourceObservation{
		{RelativePath: "Box/Disc 1/a.flac", Directory: "Box/Disc 1", Album: "Set", DiscNumber: 1},
		{RelativePath: "Box/Disc 3/b.flac", Directory: "Box/Disc 3", Album: "Set", DiscNumber: 3},
	})
	if len(got) != 2 {
		t.Fatalf("non-contiguous discs merged: %#v", got)
	}
	for _, candidate := range got {
		if candidate.Anchor.Kind != candidateBoxLeaf {
			t.Fatalf("candidate kind = %s, want box leaf", candidate.Anchor.Kind)
		}
	}
}

func TestOrganizeRootUnknownAndCueIdentityAreExplicit(t *testing.T) {
	unknown := organizeObservations([]sourceObservation{
		{RelativePath: "one.flac", Title: "One", Artist: "Artist"},
		{RelativePath: "two.flac", Title: "Two", Artist: "Artist"},
	})
	if len(unknown) != 2 || unknown[0].Title != unknownCandidateTitle || unknown[1].Title != unknownCandidateTitle {
		t.Fatalf("root unknown files were merged: %#v", unknown)
	}

	physical := sourceObservation{RelativePath: "disc.wav", SourceKind: "wav"}
	cueA := sourceObservation{RelativePath: "album.cue#track-1@disc.wav", SourceKind: "cue_virtual", CueSheetPath: "album.cue", CueParentRelativePath: "disc.wav", TrackNumber: 1, CueIndexFrames: 0, CueIndexPresent: true}
	cueB := cueA
	cueB.CueIndexFrames = 75
	if sourceIdentityForObservation("root", physical) == sourceIdentityForObservation("root", cueA) || sourceIdentityForObservation("root", cueA) == sourceIdentityForObservation("root", cueB) {
		t.Fatal("physical and CUE virtual source identities collided")
	}
}

func TestObservationFieldSourcesDistinguishFallbacks(t *testing.T) {
	observation := audioObservation{InferredFields: map[string]bool{"title": true, "artist": true, "album": true}}
	sources := observationFieldSources(observation)
	if sources["title"] != "filename_fallback" || sources["artist"] != "display_fallback" || sources["album"] != "folder_fallback" {
		t.Fatalf("unexpected fallback sources: %#v", sources)
	}
}

func TestOrganizeTagAlbumOutranksFolderFallback(t *testing.T) {
	observations := []sourceObservation{
		{RelativePath: "dir/one.flac", Directory: "dir", Album: "Tagged", InferredFields: map[string]bool{}},
		{RelativePath: "dir/two.flac", Directory: "dir", Album: "dir", InferredFields: map[string]bool{"album": true}, FieldSources: map[string]string{"album": "folder_fallback"}},
		{RelativePath: "dir/three.flac", Directory: "dir", Album: "dir", InferredFields: map[string]bool{"album": true}, FieldSources: map[string]string{"album": "folder_fallback"}},
	}
	got := organizeObservations(observations)
	if len(got) != 1 || got[0].Title != "Tagged" {
		t.Fatalf("tag album did not outrank fallback: %#v", got)
	}
}

func TestOrganizeAlbumArtistKeepsAuthoritativeProvenance(t *testing.T) {
	got := organizeObservations([]sourceObservation{{RelativePath: "album/one.flac", Directory: "album", Album: "Release", AlbumArtist: "Album Artist", Artist: "未知艺术家", InferredFields: map[string]bool{"artist": true}, FieldSources: map[string]string{"artist": "display_fallback"}}})
	if len(got) != 1 || got[0].Artist != "Album Artist" || got[0].AlbumArtist != "Album Artist" {
		t.Fatalf("album artist was not selected: %#v", got)
	}
	for _, decision := range got[0].Decisions {
		if (decision.Field == "artist" || decision.Field == "album_artist") && (decision.Confidence != "high" || decision.Source != "tag") {
			t.Fatalf("album artist provenance was downgraded: %#v", decision)
		}
	}
}

func TestOrganizeMissingAlbumArtistRemainsCompatibleWithKnownRelease(t *testing.T) {
	observations := []sourceObservation{
		{RelativePath: "album/01.flac", Directory: "album", Album: "Release", AlbumArtist: "Album Artist", Artist: "Guest One"},
		{RelativePath: "album/02.flac", Directory: "album", Album: "Release", Artist: "Track Artist"},
		{RelativePath: "album/03.flac", Directory: "album", Album: "Release", Artist: "Track Artist"},
	}
	got := organizeObservations(observations)
	if len(got) != 1 {
		t.Fatalf("missing album artist split a compatible release: %#v", got)
	}
	if got[0].AlbumArtist != "Album Artist" || got[0].Artist != "Album Artist" {
		t.Fatalf("authoritative album artist did not win release credit: %#v", got[0])
	}

	rootObservations := []sourceObservation{
		{RelativePath: "01.flac", Album: "Release", AlbumArtist: "Album Artist", Artist: "Guest One"},
		{RelativePath: "02.flac", Album: "Release", Artist: "Track Artist"},
	}
	rootGot := organizeObservations(rootObservations)
	if len(rootGot) != 1 || rootGot[0].AlbumArtist != "Album Artist" || rootGot[0].Artist != "Album Artist" {
		t.Fatalf("root album observations were not reconciled: %#v", rootGot)
	}
	if observationOrganizationScope(rootObservations[0]) != observationOrganizationScope(rootObservations[1]) {
		t.Fatal("root staging separated a missing album artist before organizer reconciliation")
	}
}

func TestResolveCuePhysicalCoexistenceUsesExplicitParent(t *testing.T) {
	image := sourceObservation{RelativePath: "album/image.flac", Directory: "album", SourceKind: "physical"}
	virtual := sourceObservation{RelativePath: "album/disc.cue#track-1@image.flac", Directory: "album", SourceKind: "cue_virtual", CueParentRelativePath: "album/image.flac"}
	if got := resolveCuePhysicalCoexistence([]sourceObservation{image, virtual}); len(got) != 1 || isCueObservation(got[0]) {
		t.Fatalf("single virtual track did not retain its physical parent: %#v", got)
	}

	unrelated := sourceObservation{RelativePath: "album/unrelated.flac", Directory: "album", SourceKind: "physical"}
	got := resolveCuePhysicalCoexistence([]sourceObservation{image, virtual, unrelated})
	if len(got) != 2 || isCueObservation(got[0]) || isCueObservation(got[1]) {
		t.Fatalf("unrelated physical source changed parent decision: %#v", got)
	}

	secondVirtual := virtual
	secondVirtual.RelativePath = "album/disc.cue#track-2@image.flac"
	got = resolveCuePhysicalCoexistence([]sourceObservation{image, virtual, secondVirtual, unrelated})
	if len(got) != 3 || !isCueObservation(got[0]) || !isCueObservation(got[1]) || got[2].RelativePath != unrelated.RelativePath {
		t.Fatalf("multi-track CUE did not independently hide only its parent: %#v", got)
	}

	secondParent := sourceObservation{RelativePath: "album/second.flac", Directory: "album", SourceKind: "physical"}
	secondParentVirtual := sourceObservation{RelativePath: "album/disc.cue#track-3@second.flac", Directory: "album", SourceKind: "cue_virtual", CueParentRelativePath: "album/second.flac"}
	got = resolveCuePhysicalCoexistence([]sourceObservation{image, virtual, secondVirtual, secondParent, secondParentVirtual})
	if len(got) != 3 || !isCueObservation(got[0]) || !isCueObservation(got[1]) || got[2].RelativePath != secondParent.RelativePath {
		t.Fatalf("per-parent CUE decisions interfered: %#v", got)
	}
}

func TestSingleCueTrackEnrichesInferredPhysicalMetadata(t *testing.T) {
	physical := sourceObservation{RelativePath: "album/image.flac", Directory: "album", Title: "image", Album: "album", Artist: "未知艺术家", SourceKind: "physical", InferredFields: map[string]bool{"title": true, "album": true, "artist": true}, FieldSources: map[string]string{"title": "filename_fallback", "album": "folder_fallback", "artist": "display_fallback"}}
	virtual := sourceObservation{RelativePath: "album/disc.cue#track-1", Directory: "album", Title: "Cue Title", Album: "Cue Album", AlbumArtist: "Cue Artist", Artist: "Cue Artist", SourceKind: "cue_virtual", CueParentRelativePath: "album/image.flac", FieldSources: map[string]string{"title": "cue_track", "album": "cue_sheet", "album_artist": "cue_sheet", "artist": "cue_track"}}
	got := resolveCueCandidateGroups([]sourceObservation{physical, virtual})
	if len(got) != 1 || isCueObservation(got[0]) || got[0].Title != "Cue Title" || got[0].Album != "Cue Album" || got[0].AlbumArtist != "Cue Artist" || got[0].Artist != "Cue Artist" {
		t.Fatalf("single CUE evidence did not enrich physical track: %#v", got)
	}
	for _, field := range []string{"title", "album", "artist"} {
		if got[0].InferredFields[field] {
			t.Fatalf("CUE-backed %s remained inferred: %#v", field, got[0])
		}
	}
}

func TestCueCandidateIncludesExplicitParentOutsideSheetDirectory(t *testing.T) {
	physical := sourceObservation{
		RelativePath:   "media/image.flac",
		Directory:      "media",
		Title:          "image",
		Album:          "media",
		Artist:         "未知艺术家",
		SourceKind:     "flac",
		InferredFields: map[string]bool{"title": true, "album": true, "artist": true},
		FieldSources:   map[string]string{"title": "filename_fallback", "album": "folder_fallback", "artist": "display_fallback"},
	}
	virtual := sourceObservation{
		RelativePath:          "sheets/album.cue#track-1@../media/image.flac",
		Directory:             "sheets",
		Title:                 "CUE Track",
		Album:                 "CUE Album",
		AlbumArtist:           "CUE Artist",
		Artist:                "CUE Artist",
		SourceKind:            "cue_virtual",
		CueSheetPath:          "sheets/album.cue",
		CueParentRelativePath: "media/image.flac",
		FieldSources:          map[string]string{"title": "cue_track", "album": "cue_sheet", "album_artist": "cue_sheet", "artist": "cue_track"},
	}

	got := resolveCueCandidateGroups([]sourceObservation{physical, virtual})
	if len(got) != 1 || isCueObservation(got[0]) || got[0].RelativePath != physical.RelativePath {
		t.Fatalf("cross-directory CUE parent was excluded from its candidate: %#v", got)
	}
	if got[0].Title != "CUE Track" || got[0].Album != "CUE Album" || got[0].AlbumArtist != "CUE Artist" {
		t.Fatalf("cross-directory CUE evidence was not merged: %#v", got[0])
	}
}

func TestSingleCueTrackRetainsCueFactsOnPhysicalIdentity(t *testing.T) {
	physical := sourceObservation{RelativePath: "album/01.flac", Directory: "album", SourceKind: "flac"}
	virtual := sourceObservation{
		RelativePath:          "album/disc.cue#track-1",
		Directory:             "album",
		SourceKind:            "cue_virtual",
		CueSheetPath:          "album/disc.cue",
		CueParentRelativePath: "album/01.flac",
		CueReferencedFile:     "01.flac",
		CueIndexFrames:        0,
		CueIndexPresent:       true,
		CueEndFrames:          750,
		CueEndPresent:         true,
		CueISRC:               "TEST00000001",
	}
	got := resolveCueCandidateGroups([]sourceObservation{physical, virtual})
	if len(got) != 1 || isCueObservation(got[0]) {
		t.Fatalf("single-file physical identity was not retained: %#v", got)
	}
	if got[0].CueSheetPath != "album/disc.cue" || !got[0].CueIndexPresent || got[0].CueIndexFrames != 0 || !got[0].CueEndPresent || got[0].CueEndFrames != 750 || got[0].CueISRC != "TEST00000001" {
		t.Fatalf("CUE facts were discarded with the virtual identity: %#v", got[0])
	}
}

func TestArtworkDimensionsRejectMalformedAndReadWebP(t *testing.T) {
	if width, height := artworkDimensions([]byte{0xff, 0xd8, 0xff}, "image/jpeg"); width != 0 || height != 0 {
		t.Fatalf("malformed JPEG dimensions = %dx%d", width, height)
	}
	webp := make([]byte, 30)
	copy(webp[:4], "RIFF")
	copy(webp[8:12], "WEBP")
	copy(webp[12:16], "VP8X")
	webp[24], webp[27] = 1, 2 // WebP 将宽高保存为实际值减一。
	if width, height := artworkDimensions(webp, "image/webp"); width != 2 || height != 3 {
		t.Fatalf("WebP dimensions = %dx%d, want 2x3", width, height)
	}
}

func TestFolderArtworkDoesNotFollowSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, minimalPNGArtwork(), 0o644); err != nil {
		t.Fatalf("write outside artwork: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "cover.png")); err != nil {
		t.Fatalf("create artwork symlink: %v", err)
	}
	if data, _, err := discoverFolderArtwork(root); err == nil || len(data) != 0 {
		t.Fatal("folder artwork symlink was not rejected")
	}
}

func TestFolderArtworkRejectsInvalidNamedCandidate(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cover.png"), []byte("not an image"), 0o644); err != nil {
		t.Fatalf("write invalid artwork: %v", err)
	}
	if data, _, err := discoverFolderArtwork(root); err == nil || len(data) != 0 {
		t.Fatal("invalid named artwork was treated as absent")
	}
}

func TestFolderArtworkReadFailureIsReported(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "cover.png"), minimalPNGArtwork(), 0o644); err != nil {
		t.Fatalf("write folder artwork: %v", err)
	}
	_, _, err := discoverFolderArtworkWithReader(root, func(string) ([]byte, error) {
		return nil, fmt.Errorf("read denied")
	})
	if err == nil {
		t.Fatal("folder artwork read failure was treated as missing artwork")
	}
}

func TestArtworkPersistenceFailureKeepsDiagnosticCategory(t *testing.T) {
	kind, message := candidatePersistenceDiagnostic(errArtworkBinding)
	if kind != "artwork_failure" || message != "封面处理失败" {
		t.Fatalf("artwork diagnostic = %q/%q", kind, message)
	}
}

func TestLegacyCueSourceIdentityCanBeReusedAfterUpgrade(t *testing.T) {
	observation := sourceObservation{
		SourceKind:            "cue_virtual",
		TrackNumber:           2,
		CueSheetPath:          "Album/album.cue",
		CueParentRelativePath: "Album/disc/image.flac",
		CueReferencedFile:     "Album/disc/image.flac",
	}
	got := legacyCueSourceIdentity("root-id", observation)
	want := "root-id:Album/album.cue#track-2@disc/image.flac"
	if got != want {
		t.Fatalf("legacy CUE source identity = %q, want %q", got, want)
	}
}

func TestOrganizerEvidenceListsAreBounded(t *testing.T) {
	observations := make([]sourceObservation, 0, maxGroupingEvidenceRefs+5)
	for index := 0; index < maxGroupingEvidenceRefs+5; index++ {
		observations = append(observations, sourceObservation{RelativePath: fmt.Sprintf("album/%03d.flac", index), Directory: "album", Album: "Release", Artist: fmt.Sprintf("Artist %03d", index)})
	}
	decision := chooseValue(observations, "artist", func(observation sourceObservation) string { return observation.Artist })
	if len(decision.Candidates) != maxDecisionCandidates {
		t.Fatalf("decision candidates = %d, want bound %d", len(decision.Candidates), maxDecisionCandidates)
	}
	candidates := organizeObservations(observations)
	if len(candidates) != 1 || len(candidates[0].GroupingRefs) != maxGroupingEvidenceRefs {
		t.Fatalf("grouping refs were not bounded: %#v", candidates)
	}
}

func TestOrganizerAssignsStableFallbackTrackPositions(t *testing.T) {
	observations := []sourceObservation{
		{RelativePath: "album/c.flac", Directory: "album", Album: "Release", TrackNumber: 1, InferredFields: map[string]bool{"track_number": true}},
		{RelativePath: "album/a.flac", Directory: "album", Album: "Release", TrackNumber: 1, InferredFields: map[string]bool{"track_number": true}},
		{RelativePath: "album/b.flac", Directory: "album", Album: "Release", TrackNumber: 1, InferredFields: map[string]bool{"track_number": true}},
	}
	got := organizeObservations(observations)
	if len(got) != 1 || len(got[0].Mediums[1]) != 3 {
		t.Fatalf("unexpected candidates: %#v", got)
	}
	for index, track := range got[0].Mediums[1] {
		if track.Position != index+1 || track.Observation.TrackNumber != index+1 || track.Observation.FieldSources["track_number"] != "path_order_fallback" || track.Observation.RelativePath != fmt.Sprintf("album/%c.flac", 'a'+rune(index)) {
			t.Fatalf("fallback order at %d: %#v", index, track)
		}
	}
}

func TestOrganizerFallbackPositionsPreserveExplicitTags(t *testing.T) {
	observations := []sourceObservation{
		{RelativePath: "album/c.flac", Directory: "album", Album: "Release", TrackNumber: 1, InferredFields: map[string]bool{"track_number": true}},
		{RelativePath: "album/b.flac", Directory: "album", Album: "Release", TrackNumber: 2},
		{RelativePath: "album/a.flac", Directory: "album", Album: "Release", TrackNumber: 1, InferredFields: map[string]bool{"track_number": true}},
	}
	got := organizeObservations(observations)
	if len(got) != 1 || len(got[0].Mediums[1]) != 3 {
		t.Fatalf("unexpected candidates: %#v", got)
	}
	for index, track := range got[0].Mediums[1] {
		if track.Position != index+1 || track.Observation.RelativePath != fmt.Sprintf("album/%c.flac", 'a'+rune(index)) {
			t.Fatalf("mixed explicit/fallback order at %d: %#v", index, track)
		}
	}
	if got[0].Mediums[1][1].Observation.FieldSources["track_number"] == "path_order_fallback" {
		t.Fatalf("explicit track position was overwritten: %#v", got[0].Mediums[1][1])
	}

	explicitDisc := organizeObservations([]sourceObservation{{
		RelativePath: "Box/CD2/01.flac",
		Directory:    "Box/CD2",
		Album:        "Release",
		TrackNumber:  1,
		DiscNumber:   7,
	}})
	if len(explicitDisc) != 1 || len(explicitDisc[0].Mediums[7]) != 1 || len(explicitDisc[0].Mediums[2]) != 0 {
		t.Fatalf("explicit disc position was overwritten by folder structure: %#v", explicitDisc)
	}
}

func TestRipLogEvidenceFillsOnlyMissingReleaseSemantics(t *testing.T) {
	candidates := organizeObservations([]sourceObservation{{
		RelativePath: "album/01.flac",
		Directory:    "album",
		Album:        "Release",
		Artist:       "Artist",
		SourceType:   "Vinyl",
		MediaType:    "LP",
	}})
	if len(candidates) != 1 {
		t.Fatalf("unexpected candidates: %#v", candidates)
	}
	applyRipLogEvidence(&candidates[0], []string{"album/rip.log"})
	if candidates[0].SourceType != "Vinyl" || candidates[0].MediaType != "LP" {
		t.Fatalf("rip log overrode explicit tags: %#v", candidates[0])
	}

	candidates = organizeObservations([]sourceObservation{{RelativePath: "album/01.flac", Directory: "album", Album: "Release", Artist: "Artist"}})
	applyRipLogEvidence(&candidates[0], []string{"album/rip.log"})
	if candidates[0].SourceType != "CD" || candidates[0].MediaType != "CD" || candidates[0].GroupingRefs[0] != "album/rip.log" {
		t.Fatalf("explicit rip log evidence was not applied: %#v", candidates[0])
	}
	for _, field := range []string{"source_type", "media_type"} {
		found := false
		for _, decision := range candidates[0].Decisions {
			if decision.Field == field && decision.Source == "rip_log" && decision.Confidence == "high" && decision.Action == "auto_apply" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %s rip-log decision: %#v", field, candidates[0].Decisions)
		}
	}

	folderCandidate := organizedCandidate{
		SourceType: "WEB",
		MediaType:  "Digital Media",
		Decisions: []fieldDecision{
			{Field: "source_type", Value: "WEB", Source: "folder", RuleID: "majority_v1"},
			{Field: "media_type", Value: "Digital Media", Source: "folder", RuleID: "majority_v1"},
		},
	}
	applyRipLogEvidence(&folderCandidate, []string{"album/rip.log"})
	if folderCandidate.SourceType != "CD" || folderCandidate.MediaType != "CD" {
		t.Fatalf("明确抓轨日志没有覆盖较弱目录补充值：%#v", folderCandidate)
	}
	for _, field := range []string{"source_type", "media_type"} {
		matches := 0
		for _, decision := range folderCandidate.Decisions {
			if decision.Field != field {
				continue
			}
			matches++
			if decision.Source != "rip_log" || decision.RuleID != "rip_log_cd_v1" {
				t.Fatalf("%s 仍保留较弱目录 provenance：%#v", field, decision)
			}
		}
		if matches != 1 {
			t.Fatalf("%s 决策数量 = %d，期望 1：%#v", field, matches, folderCandidate.Decisions)
		}
	}
}

func TestDiscoverCandidateRipLogEvidenceRequiresExplicitSignatureAndSkipsSymlink(t *testing.T) {
	rootPath := t.TempDir()
	albumPath := filepath.Join(rootPath, "Album")
	if err := os.Mkdir(albumPath, 0o755); err != nil {
		t.Fatalf("create album directory: %v", err)
	}
	for name, content := range map[string]string{
		"eac.LOG":   "Exact Audio Copy V1.6\nEAC extraction logfile",
		"notes.log": "CD FLAC 44.1 kHz without a ripping-tool signature",
		"late.log":  strings.Repeat("x", maxRipLogHeaderBytes) + "Exact Audio Copy V1.6",
		"xld.log":   "X Lossless Decoder version 2026\nAll tracks ripped",
	} {
		if err := os.WriteFile(filepath.Join(albumPath, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.log")
	if err := os.WriteFile(outside, []byte("Exact Audio Copy"), 0o644); err != nil {
		t.Fatalf("write outside log: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(albumPath, "linked.log")); err != nil {
		t.Fatalf("create log symlink: %v", err)
	}

	candidate := organizedCandidate{
		Anchor:  candidateAnchor{Kind: candidateOrdinary, Scope: "Album"},
		Mediums: map[int][]organizedTrack{1: {{Observation: sourceObservation{RelativePath: "Album/01.flac", Directory: "Album"}}}},
	}
	references, diagnostics := discoverCandidateRipLogEvidence(registeredRoot{ID: "root", Path: rootPath}, candidate)
	if !reflect.DeepEqual(references, []string{"Album/eac.LOG", "Album/xld.log"}) || len(diagnostics) != 0 {
		t.Fatalf("unexpected rip-log evidence: refs=%v diagnostics=%v", references, diagnostics)
	}
}
