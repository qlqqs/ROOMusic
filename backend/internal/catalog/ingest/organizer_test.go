package ingest

import (
	"fmt"
	"path"
	"reflect"
	"slices"
	"testing"
)

func TestOrganizeLooseAndUnknown(t *testing.T) {
	got := OrganizeObservations([]SourceObservation{{RelativePath: "a.flac", Album: "Album", TrackNumber: 1}, {RelativePath: "b.flac", Album: "Album", TrackNumber: 2}, {RelativePath: "c.flac", Title: "x"}})
	if len(got) != 2 {
		t.Fatalf("candidates=%d", len(got))
	}
	if got[0].Anchor.Kind != CandidateLooseAlbum && got[1].Anchor.Kind != CandidateLooseAlbum {
		t.Fatalf("missing loose album candidate: %#v", got)
	}
}

func TestOrganizeMajorityDeterministic(t *testing.T) {
	a := []SourceObservation{{RelativePath: "z.flac", Album: "Same", AlbumArtist: "B"}, {RelativePath: "a.flac", Album: "Same", AlbumArtist: "A"}, {RelativePath: "m.flac", Album: "Other", AlbumArtist: "A"}}
	b := []SourceObservation{a[2], a[0], a[1]}
	x, y := OrganizeObservations(a), OrganizeObservations(b)
	if len(x) != len(y) || x[0].Title != y[0].Title || x[0].Artist != y[0].Artist {
		t.Fatalf("order dependent: %#v %#v", x, y)
	}
}

func TestOrganizeStrictMultidisc(t *testing.T) {
	obs := []SourceObservation{{RelativePath: "Disc 1/a.flac", Directory: "", Album: "Box", DiscNumber: 1, TrackNumber: 1}, {RelativePath: "CD2/b.flac", Album: "Box", DiscNumber: 2, TrackNumber: 1}}
	got := OrganizeObservations(obs)
	if len(got) != 1 {
		t.Fatalf("got %d candidates", len(got))
	}
	if got[0].Anchor.Kind != CandidateMultiDisc {
		t.Fatalf("kind=%s", got[0].Anchor.Kind)
	}
	if len(got[0].Mediums) != 2 {
		t.Fatalf("mediums=%d", len(got[0].Mediums))
	}
}

func TestOrganizeStrictMultidiscUsesPhysicalDiscFolderEvidence(t *testing.T) {
	parent := "Artist - Album (2024) [CAT-1001]"
	got := OrganizeObservations([]SourceObservation{
		{RelativePath: parent + "/CD1/01.flac", Directory: parent + "/CD1", Album: "Album", Artist: "Artist", DiscNumber: 1, TrackNumber: 1},
		{RelativePath: parent + "/CD2/01.flac", Directory: parent + "/CD2", Album: "Album", Artist: "Artist", DiscNumber: 2, TrackNumber: 1},
	})
	if len(got) != 1 || got[0].Anchor.Kind != CandidateMultiDisc {
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
	got := OrganizeObservations([]SourceObservation{
		{RelativePath: "Box/Disc 1/a.flac", Directory: "Box/Disc 1", Album: "Disc 1", InferredFields: map[string]bool{"album": true, "disc_number": true}, DiscNumber: 1},
		{RelativePath: "Box/CD2/b.flac", Directory: "Box/CD2", Album: "CD2", InferredFields: map[string]bool{"album": true, "disc_number": true}, DiscNumber: 1},
	})
	if len(got) != 1 || got[0].Anchor.Kind != CandidateMultiDisc || got[0].Title != "Box" {
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
	majority := OrganizeObservations([]SourceObservation{
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

	tie := []SourceObservation{
		{RelativePath: "album/02.flac", Directory: "album", Album: "Beta", AlbumArtist: "B"},
		{RelativePath: "album/01.flac", Directory: "album", Album: "Alpha", AlbumArtist: "A"},
	}
	got := OrganizeObservations(tie)
	reversed := OrganizeObservations([]SourceObservation{tie[1], tie[0]})
	if len(got) != 2 || !reflect.DeepEqual(got, reversed) {
		t.Fatalf("tie did not produce stable split: %#v %#v", got, reversed)
	}
	for _, candidate := range got {
		if candidate.Anchor.Kind != CandidateSameDirSplit || candidate.Anchor.Partition == "" {
			t.Fatalf("unexpected split candidate: %#v", candidate.Anchor)
		}
		if !reflect.DeepEqual(candidate.Attention, []string{"same_directory_conflict"}) {
			t.Fatalf("split grouping attention = %#v", candidate.Attention)
		}
	}
}

func TestOrganizeNonContiguousDiscDirectoriesStayLeafCandidates(t *testing.T) {
	got := OrganizeObservations([]SourceObservation{
		{RelativePath: "Box/Disc 1/a.flac", Directory: "Box/Disc 1", Album: "Set", DiscNumber: 1},
		{RelativePath: "Box/Disc 3/b.flac", Directory: "Box/Disc 3", Album: "Set", DiscNumber: 3},
	})
	if len(got) != 2 {
		t.Fatalf("non-contiguous discs merged: %#v", got)
	}
	for _, candidate := range got {
		if candidate.Anchor.Kind != CandidateBoxLeaf {
			t.Fatalf("candidate kind = %s, want box leaf", candidate.Anchor.Kind)
		}
	}
}

func TestOrganizeRootUnknownAndCueIdentityAreExplicit(t *testing.T) {
	unknown := OrganizeObservations([]SourceObservation{
		{RelativePath: "one.flac", Title: "One", Artist: "Artist"},
		{RelativePath: "two.flac", Title: "Two", Artist: "Artist"},
	})
	if len(unknown) != 2 || unknown[0].Title != unknownCandidateTitle || unknown[1].Title != unknownCandidateTitle {
		t.Fatalf("root unknown files were merged: %#v", unknown)
	}

	physical := SourceObservation{RelativePath: "disc.wav", SourceKind: "wav"}
	cueA := SourceObservation{RelativePath: "album.cue#track-1@disc.wav", SourceKind: "cue_virtual", CueSheetPath: "album.cue", CueParentRelativePath: "disc.wav", TrackNumber: 1, CueIndexFrames: 0, CueIndexPresent: true}
	cueB := cueA
	cueB.CueIndexFrames = 75
	if SourceIdentityForObservation("root", physical) == SourceIdentityForObservation("root", cueA) || SourceIdentityForObservation("root", cueA) == SourceIdentityForObservation("root", cueB) {
		t.Fatal("physical and CUE virtual source identities collided")
	}
}

func TestOrganizeTagAlbumOutranksFolderFallback(t *testing.T) {
	observations := []SourceObservation{
		{RelativePath: "dir/one.flac", Directory: "dir", Album: "Tagged", InferredFields: map[string]bool{}},
		{RelativePath: "dir/two.flac", Directory: "dir", Album: "dir", InferredFields: map[string]bool{"album": true}, FieldSources: map[string]string{"album": "folder_fallback"}},
		{RelativePath: "dir/three.flac", Directory: "dir", Album: "dir", InferredFields: map[string]bool{"album": true}, FieldSources: map[string]string{"album": "folder_fallback"}},
	}
	got := OrganizeObservations(observations)
	if len(got) != 1 || got[0].Title != "Tagged" {
		t.Fatalf("tag album did not outrank fallback: %#v", got)
	}
}

func TestOrganizeAlbumArtistKeepsAuthoritativeProvenance(t *testing.T) {
	got := OrganizeObservations([]SourceObservation{{RelativePath: "album/one.flac", Directory: "album", Album: "Release", AlbumArtist: "Album Artist", Artist: "未知艺术家", InferredFields: map[string]bool{"artist": true}, FieldSources: map[string]string{"artist": "display_fallback"}}})
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
	observations := []SourceObservation{
		{RelativePath: "album/01.flac", Directory: "album", Album: "Release", AlbumArtist: "Album Artist", Artist: "Guest One"},
		{RelativePath: "album/02.flac", Directory: "album", Album: "Release", Artist: "Track Artist"},
		{RelativePath: "album/03.flac", Directory: "album", Album: "Release", Artist: "Track Artist"},
	}
	got := OrganizeObservations(observations)
	if len(got) != 1 {
		t.Fatalf("missing album artist split a compatible release: %#v", got)
	}
	if got[0].AlbumArtist != "Album Artist" || got[0].Artist != "Album Artist" {
		t.Fatalf("authoritative album artist did not win release credit: %#v", got[0])
	}

	rootObservations := []SourceObservation{
		{RelativePath: "01.flac", Album: "Release", AlbumArtist: "Album Artist", Artist: "Guest One"},
		{RelativePath: "02.flac", Album: "Release", Artist: "Track Artist"},
	}
	rootGot := OrganizeObservations(rootObservations)
	if len(rootGot) != 1 || rootGot[0].AlbumArtist != "Album Artist" || rootGot[0].Artist != "Album Artist" {
		t.Fatalf("root album observations were not reconciled: %#v", rootGot)
	}
	if OrganizationScope(rootObservations[0]) != OrganizationScope(rootObservations[1]) {
		t.Fatal("root staging separated a missing album artist before organizer reconciliation")
	}
}

func TestResolveCuePhysicalCoexistenceUsesExplicitParent(t *testing.T) {
	image := SourceObservation{RelativePath: "album/image.flac", Directory: "album", SourceKind: "physical"}
	virtual := SourceObservation{RelativePath: "album/disc.cue#track-1@image.flac", Directory: "album", SourceKind: "cue_virtual", CueParentRelativePath: "album/image.flac"}
	if got := resolveCuePhysicalCoexistence([]SourceObservation{image, virtual}); len(got) != 1 || isCueObservation(got[0]) {
		t.Fatalf("single virtual track did not retain its physical parent: %#v", got)
	}

	unrelated := SourceObservation{RelativePath: "album/unrelated.flac", Directory: "album", SourceKind: "physical"}
	got := resolveCuePhysicalCoexistence([]SourceObservation{image, virtual, unrelated})
	if len(got) != 2 || isCueObservation(got[0]) || isCueObservation(got[1]) {
		t.Fatalf("unrelated physical source changed parent decision: %#v", got)
	}

	secondVirtual := virtual
	secondVirtual.RelativePath = "album/disc.cue#track-2@image.flac"
	got = resolveCuePhysicalCoexistence([]SourceObservation{image, virtual, secondVirtual, unrelated})
	if len(got) != 3 || !isCueObservation(got[0]) || !isCueObservation(got[1]) || got[2].RelativePath != unrelated.RelativePath {
		t.Fatalf("multi-track CUE did not independently hide only its parent: %#v", got)
	}

	secondParent := SourceObservation{RelativePath: "album/second.flac", Directory: "album", SourceKind: "physical"}
	secondParentVirtual := SourceObservation{RelativePath: "album/disc.cue#track-3@second.flac", Directory: "album", SourceKind: "cue_virtual", CueParentRelativePath: "album/second.flac"}
	got = resolveCuePhysicalCoexistence([]SourceObservation{image, virtual, secondVirtual, secondParent, secondParentVirtual})
	if len(got) != 3 || !isCueObservation(got[0]) || !isCueObservation(got[1]) || got[2].RelativePath != secondParent.RelativePath {
		t.Fatalf("per-parent CUE decisions interfered: %#v", got)
	}
}

func TestSingleCueTrackEnrichesInferredPhysicalMetadata(t *testing.T) {
	physical := SourceObservation{RelativePath: "album/image.flac", Directory: "album", Title: "image", Album: "album", Artist: "未知艺术家", SourceKind: "physical", InferredFields: map[string]bool{"title": true, "album": true, "artist": true}, FieldSources: map[string]string{"title": "filename_fallback", "album": "folder_fallback", "artist": "display_fallback"}}
	virtual := SourceObservation{RelativePath: "album/disc.cue#track-1", Directory: "album", Title: "Cue Title", Album: "Cue Album", AlbumArtist: "Cue Artist", Artist: "Cue Artist", SourceKind: "cue_virtual", CueParentRelativePath: "album/image.flac", FieldSources: map[string]string{"title": "cue_track", "album": "cue_sheet", "album_artist": "cue_sheet", "artist": "cue_track"}}
	got := resolveCueCandidateGroups([]SourceObservation{physical, virtual})
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
	physical := SourceObservation{
		RelativePath:   "media/image.flac",
		Directory:      "media",
		Title:          "image",
		Album:          "media",
		Artist:         "未知艺术家",
		SourceKind:     "flac",
		InferredFields: map[string]bool{"title": true, "album": true, "artist": true},
		FieldSources:   map[string]string{"title": "filename_fallback", "album": "folder_fallback", "artist": "display_fallback"},
	}
	virtual := SourceObservation{
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

	got := resolveCueCandidateGroups([]SourceObservation{physical, virtual})
	if len(got) != 1 || isCueObservation(got[0]) || got[0].RelativePath != physical.RelativePath {
		t.Fatalf("cross-directory CUE parent was excluded from its candidate: %#v", got)
	}
	if got[0].Title != "CUE Track" || got[0].Album != "CUE Album" || got[0].AlbumArtist != "CUE Artist" {
		t.Fatalf("cross-directory CUE evidence was not merged: %#v", got[0])
	}
}

func TestSingleCueTrackRetainsCueFactsOnPhysicalIdentity(t *testing.T) {
	physical := SourceObservation{RelativePath: "album/01.flac", Directory: "album", SourceKind: "flac"}
	virtual := SourceObservation{
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
	got := resolveCueCandidateGroups([]SourceObservation{physical, virtual})
	if len(got) != 1 || isCueObservation(got[0]) {
		t.Fatalf("single-file physical identity was not retained: %#v", got)
	}
	if got[0].CueSheetPath != "album/disc.cue" || !got[0].CueIndexPresent || got[0].CueIndexFrames != 0 || !got[0].CueEndPresent || got[0].CueEndFrames != 750 || got[0].CueISRC != "TEST00000001" {
		t.Fatalf("CUE facts were discarded with the virtual identity: %#v", got[0])
	}
}

func TestOrganizerEvidenceListsAreBounded(t *testing.T) {
	observations := make([]SourceObservation, 0, MaxGroupingEvidenceRefs+5)
	for index := 0; index < MaxGroupingEvidenceRefs+5; index++ {
		observations = append(observations, SourceObservation{RelativePath: fmt.Sprintf("album/%03d.flac", index), Directory: "album", Album: "Release", Artist: fmt.Sprintf("Artist %03d", index)})
	}
	decision := chooseValue(observations, "artist", func(observation SourceObservation) string { return observation.Artist })
	if len(decision.Candidates) != maxDecisionCandidates {
		t.Fatalf("decision candidates = %d, want bound %d", len(decision.Candidates), maxDecisionCandidates)
	}
	candidates := OrganizeObservations(observations)
	if len(candidates) != 1 || len(candidates[0].GroupingRefs) != MaxGroupingEvidenceRefs {
		t.Fatalf("grouping refs were not bounded: %#v", candidates)
	}
}

func TestOrganizerAssignsStableFallbackTrackPositions(t *testing.T) {
	observations := []SourceObservation{
		{RelativePath: "album/c.flac", Directory: "album", Album: "Release", TrackNumber: 1, InferredFields: map[string]bool{"track_number": true}},
		{RelativePath: "album/a.flac", Directory: "album", Album: "Release", TrackNumber: 1, InferredFields: map[string]bool{"track_number": true}},
		{RelativePath: "album/b.flac", Directory: "album", Album: "Release", TrackNumber: 1, InferredFields: map[string]bool{"track_number": true}},
	}
	got := OrganizeObservations(observations)
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
	observations := []SourceObservation{
		{RelativePath: "album/c.flac", Directory: "album", Album: "Release", TrackNumber: 1, InferredFields: map[string]bool{"track_number": true}},
		{RelativePath: "album/b.flac", Directory: "album", Album: "Release", TrackNumber: 2},
		{RelativePath: "album/a.flac", Directory: "album", Album: "Release", TrackNumber: 1, InferredFields: map[string]bool{"track_number": true}},
	}
	got := OrganizeObservations(observations)
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

	explicitDisc := OrganizeObservations([]SourceObservation{{
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
	candidates := OrganizeObservations([]SourceObservation{{
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
	ApplyRipLogEvidence(&candidates[0], []string{"album/rip.log"})
	if candidates[0].SourceType != "Vinyl" || candidates[0].MediaType != "LP" {
		t.Fatalf("rip log overrode explicit tags: %#v", candidates[0])
	}

	candidates = OrganizeObservations([]SourceObservation{{RelativePath: "album/01.flac", Directory: "album", Album: "Release", Artist: "Artist"}})
	ApplyRipLogEvidence(&candidates[0], []string{"album/rip.log"})
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

	folderCandidate := OrganizedCandidate{
		SourceType: "WEB",
		MediaType:  "Digital Media",
		Decisions: []FieldDecision{
			{Field: "source_type", Value: "WEB", Source: "folder", RuleID: "majority_v1"},
			{Field: "media_type", Value: "Digital Media", Source: "folder", RuleID: "majority_v1"},
		},
	}
	ApplyRipLogEvidence(&folderCandidate, []string{"album/rip.log"})
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

func TestOrganizeGenresUnionDoesNotFlagDifferentTrackGenres(t *testing.T) {
	observations := []SourceObservation{
		{RelativePath: "album/01.flac", Directory: "album", Album: "专辑", Artist: "艺人", Genres: []string{"国语流行乐", "Pop"}, Label: "厂牌 A"},
		{RelativePath: "album/02.flac", Directory: "album", Album: "专辑", Artist: "艺人", Genres: []string{"国语流行乐", "ＰＯＰ"}, Label: "厂牌 A"},
		{RelativePath: "album/03.flac", Directory: "album", Album: "专辑", Artist: "艺人", Genres: []string{"台语流行乐", "Pop"}, Label: "厂牌 B"},
	}
	candidates := OrganizeObservations(observations)
	if len(candidates) != 1 {
		t.Fatalf("候选数量 = %d，期望 1", len(candidates))
	}
	want := []string{"Pop", "国语流行乐", "台语流行乐"}
	if !reflect.DeepEqual(candidates[0].Genres, want) {
		t.Fatalf("专辑流派 = %#v，期望 %#v", candidates[0].Genres, want)
	}
	var genre, label FieldDecision
	for _, decision := range candidates[0].Decisions {
		switch decision.Field {
		case "genre":
			genre = decision
		case "label":
			label = decision
		}
	}
	if genre.Value != "Pop" || !reflect.DeepEqual(genre.Candidates, want) || genre.Confidence != "high" || genre.Action != "auto_apply" || genre.RuleID != "genre_union_v1" || genre.Reason != "" || genre.Source != "tag" {
		t.Fatalf("流派决定不应产生不一致问题：%+v", genre)
	}
	if label.Action != "uncertain_apply" || label.Reason != "inconsistent_candidates" {
		t.Fatalf("厂牌不一致仍应产生问题：%+v", label)
	}
}

func TestChooseGenresCountsTracksAndUsesStableSpelling(t *testing.T) {
	observations := []SourceObservation{
		{Genres: []string{"ＰＯＰ", "pop", "Pop", "Rock"}},
		{Genres: []string{"Pop", "Rock"}, FieldSources: map[string]string{"genre": "cue_sheet"}},
	}
	got := chooseGenres(observations)
	want := []string{"Pop", "Rock"}
	if !reflect.DeepEqual(got.Candidates, want) || got.Source != "cue_sheet" {
		t.Fatalf("每轨应只计一票，同票写法按名称选择并保留来源：%+v", got)
	}
	if reversed := chooseGenres([]SourceObservation{observations[1], observations[0]}); !reflect.DeepEqual(got, reversed) {
		t.Fatalf("流派决定受遍历顺序影响：%+v / %+v", got, reversed)
	}
	observations[0].Genres = append(observations[0].Genres, "Jazz")
	observations[1].Genres = []string{"Rock"}
	if got = chooseGenres(observations); !reflect.DeepEqual(got.Candidates, []string{"Rock", "Jazz", "ＰＯＰ"}) {
		t.Fatalf("同轨重复写法不应增加票数：%+v", got)
	}
}

func TestChooseGenresBoundsAndMissingValues(t *testing.T) {
	genres := make([]string, maxDecisionCandidates+2)
	for i := range genres {
		genres[i] = fmt.Sprintf("Genre %02d", len(genres)-1-i)
	}
	got := chooseGenres([]SourceObservation{{Genres: genres}})
	if len(got.Candidates) != maxDecisionCandidates || got.Value != "Genre 00" || got.Candidates[maxDecisionCandidates-1] != "Genre 19" {
		t.Fatalf("流派列表应先排序再限制为 20 项：%+v", got)
	}
	candidates := OrganizeObservations([]SourceObservation{{RelativePath: "album/01.flac", Directory: "album", Album: "专辑"}})
	if len(candidates) != 1 || len(candidates[0].Genres) != 0 {
		t.Fatalf("无流派的候选不应生成流派值：%#v", candidates)
	}
	for _, decision := range candidates[0].Decisions {
		if decision.Field == "genre" {
			t.Fatalf("无流派时不应写入字段决定：%+v", decision)
		}
	}
}

func TestSingleCueGenresReplaceEmbeddedListWhenPresent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		genres []string
		want   []string
		source string
	}{
		{"非空列表整体覆盖", []string{"国语流行乐", "台语流行乐"}, []string{"国语流行乐", "台语流行乐"}, "cue_sheet"},
		{"空列表保留内嵌", nil, []string{"内嵌旧流派"}, "tag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			physical := SourceObservation{
				RelativePath: "album/01.flac", Directory: "album", Album: "专辑", SourceKind: "flac",
				Genres: []string{"内嵌旧流派"}, FieldSources: map[string]string{"genre": "tag"},
			}
			cue := SourceObservation{
				RelativePath: "album/disc.cue#track-1", Directory: "album", Album: "专辑", SourceKind: "cue_virtual",
				CueSheetPath: "album/disc.cue", CueParentRelativePath: physical.RelativePath,
				Genres: tc.genres, FieldSources: map[string]string{"genre": "cue_sheet"},
			}
			got := resolveCueCandidateGroups([]SourceObservation{physical, cue})
			if len(got) != 1 || got[0].RelativePath != physical.RelativePath || isCueObservation(got[0]) {
				t.Fatalf("单轨 CUE 共存应保留物理身份：%#v", got)
			}
			if !reflect.DeepEqual(got[0].Genres, tc.want) || got[0].FieldSources["genre"] != tc.source {
				t.Fatalf("流派列表及来源 = %#v / %q，期望 %#v / %q", got[0].Genres, got[0].FieldSources["genre"], tc.want, tc.source)
			}
		})
	}
}

func TestOrganizeDiscDirectoryDecisionTable(t *testing.T) {
	type folder struct {
		name, album, artist string
		disc                int
		exclusive           bool
	}
	type wantCandidate struct {
		kind                 CandidateKind
		scope, title, artist string
		discs                []int
		titles               map[int]string
	}
	cases := []struct {
		name    string
		folders []folder
		direct  []SourceObservation
		want    []wantCandidate
	}{
		{"Scans and Artwork contain no audio observations", nil, nil, nil},
		{"plain CD directories with conflicting disc tags", []folder{{"A/CD1", "Album", "Artist", 8, true}, {"A/CD2", "Album", "Artist", 9, true}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "Album", "Artist", []int{1, 2}, map[int]string{}}}},
		{"plain CD directories without tags", []folder{{"A/CD1", "", "", 0, true}, {"A/CD2", "", "", 0, true}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "A", "", []int{1, 2}, map[int]string{}}}},
		{"existing Disc 1 and Disc 2", []folder{{"A/Disc 1", "Album", "Artist", 1, false}, {"A/Disc 2", "Album", "Artist", 2, false}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "Album", "Artist", []int{1, 2}, map[int]string{}}}},
		{"English number words", []folder{{"A/Disc One", "Album", "Artist", 1, true}, {"A/Disc Two", "Album", "Artist", 2, true}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "Album", "Artist", []int{1, 2}, map[int]string{}}}},
		{"bracketed markers", []folder{{"A/[Disc 1]", "Album", "Artist", 1, true}, {"A/[Disc 2]", "Album", "Artist", 2, true}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "Album", "Artist", []int{1, 2}, map[int]string{}}}},
		{"of-total markers", []folder{{"A/Disc 1 of 2", "Album", "Artist", 1, true}, {"A/Disc 2 of 2", "Album", "Artist", 2, true}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "Album", "Artist", []int{1, 2}, map[int]string{}}}},
		{"CD1 bonus suffix", []folder{{"A/CD1", "Album", "Artist", 1, true}, {"A/CD2", "Album", "Artist", 2, true}, {"A/CD1 Bonus", "Album", "Artist", 1, true}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "Album", "Artist", []int{1, 2, 3}, map[int]string{3: "Bonus"}}}},
		{"bonus CD prefix", []folder{{"A/CD1", "Album", "Artist", 1, true}, {"A/CD2", "Album", "Artist", 2, true}, {"A/Bonus CD1", "Album", "Artist", 1, true}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "Album", "Artist", []int{1, 2, 3}, map[int]string{3: "Bonus"}}}},
		{"repeated titled positive discs", []folder{{"A/CD1", "Album", "Artist", 1, true}, {"A/CD2", "Album", "Artist", 2, true}, {"A/CD1 (Live)", "Album", "Artist", 1, true}, {"A/CD2 (Live)", "Album", "Artist", 2, true}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "Album", "Artist", []int{1, 2, 3, 4}, map[int]string{3: "Live", 4: "Live"}}}},
		{"duplicate subtitles without a plain positive disc", []folder{{"A/Disc 1 - 夏", "Album", "Artist", 1, true}, {"A/Disc 2 - 冬", "Album", "Artist", 2, true}, {"A/Disc 1 - 春", "Album", "Artist", 1, true}}, nil, []wantCandidate{{CandidateBoxLeaf, "A/Disc 1 - 夏", "Album", "Artist", []int{1}, map[int]string{}}, {CandidateBoxLeaf, "A/Disc 1 - 春", "Album", "Artist", []int{1}, map[int]string{}}, {CandidateBoxLeaf, "A/Disc 2 - 冬", "Album", "Artist", []int{2}, map[int]string{}}}},
		{"labeled gap", []folder{{"A/CD1", "Album", "Artist", 1, true}, {"A/CD3", "Album", "Artist", 3, true}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "Album", "Artist", []int{1, 3}, map[int]string{}}}},
		{"unlabeled gap", []folder{{"A/CD1", "", "", 0, true}, {"A/CD3", "", "", 0, true}}, nil, []wantCandidate{{CandidateBoxLeaf, "A/CD1", "CD1", "", []int{1}, map[int]string{}}, {CandidateBoxLeaf, "A/CD3", "CD3", "", []int{3}, map[int]string{}}}},
		{"partially labeled gap", []folder{{"A/CD1", "Album", "Artist", 1, true}, {"A/CD3", "", "", 0, true}}, nil, []wantCandidate{{CandidateBoxLeaf, "A/CD1", "Album", "Artist", []int{1}, map[int]string{}}, {CandidateBoxLeaf, "A/CD3", "CD3", "", []int{3}, map[int]string{}}}},
		{"gap starts at two", []folder{{"A/CD2", "Album", "Artist", 2, true}, {"A/CD3", "Album", "Artist", 3, true}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "Album", "Artist", []int{2, 3}, map[int]string{}}}},
		{"one exclusive disc fallback", []folder{{"A/CD1", "", "", 0, true}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "A", "", []int{1}, map[int]string{}}}},
		{"one disc plus direct audio", []folder{{"A/CD1", "", "", 0, false}}, []SourceObservation{{RelativePath: "A/direct.flac", Directory: "A"}}, []wantCandidate{{CandidateBoxLeaf, "A/CD1", "CD1", "", []int{1}, map[int]string{}}, {CandidateOrdinary, "A", "A", "", []int{1}, map[int]string{}}}},
		{"nonexclusive prefixed disc in artist folder", []folder{{"艺人/某精选 CD1", "", "", 0, false}}, []SourceObservation{{RelativePath: "艺人/另一张专辑/01.flac", Directory: "艺人/另一张专辑"}}, []wantCandidate{{CandidateBoxLeaf, "艺人/某精选 CD1", "某精选", "", []int{1}, map[int]string{}}, {CandidateOrdinary, "艺人/另一张专辑", "另一张专辑", "", []int{1}, map[int]string{}}}},
		{"nested audio makes parent nonexclusive", []folder{{"艺人/某精选 CD1", "", "", 0, false}}, []SourceObservation{{RelativePath: "艺人/某精选 CD1/附录/01.flac", Directory: "艺人/某精选 CD1/附录"}}, []wantCandidate{{CandidateBoxLeaf, "艺人/某精选 CD1", "某精选", "", []int{1}, map[int]string{}}, {CandidateOrdinary, "艺人/某精选 CD1/附录", "附录", "", []int{1}, map[int]string{}}}},
		{"nonexclusive subtitled discs stay leaves", []folder{{"艺人/Disc 1 - 夏", "Album", "Artist", 1, false}, {"艺人/Disc 2 - 冬", "Album", "Artist", 2, false}}, []SourceObservation{{RelativePath: "艺人/另一张专辑/01.flac", Directory: "艺人/另一张专辑"}}, []wantCandidate{{CandidateBoxLeaf, "艺人/Disc 1 - 夏", "Album", "Artist", []int{1}, map[int]string{}}, {CandidateBoxLeaf, "艺人/Disc 2 - 冬", "Album", "Artist", []int{2}, map[int]string{}}, {CandidateOrdinary, "艺人/另一张专辑", "另一张专辑", "", []int{1}, map[int]string{}}}},
		{"plain discs with sibling bonus directory", []folder{{"A/CD1", "Album", "Artist", 1, false}, {"A/CD2", "Album", "Artist", 2, false}}, []SourceObservation{{RelativePath: "A/Bonus/01.flac", Directory: "A/Bonus"}}, []wantCandidate{{CandidateMultiDisc, "A", "Album", "Artist", []int{1, 2}, map[int]string{}}, {CandidateOrdinary, "A/Bonus", "Bonus", "", []int{1}, map[int]string{}}}},
		{"prefixed single disc fallback", []folder{{"Box/A CD1", "", "", 0, true}}, nil, []wantCandidate{{CandidateMultiDisc, "Box", "A", "", []int{1}, map[int]string{}}}},
		{"conflicting album tags stay split", []folder{{"A/CD1", "First", "Artist", 1, true}, {"A/CD2", "Second", "Artist", 2, true}}, nil, []wantCandidate{{CandidateBoxLeaf, "A/CD1", "First", "Artist", []int{1}, map[int]string{}}, {CandidateBoxLeaf, "A/CD2", "Second", "Artist", []int{2}, map[int]string{}}}},
		{"album title disc markers normalize and group", []folder{{"A/CD1", "X (Disc 1)", "Artist", 1, false}, {"A/CD2", "X (Disc 2)", "Artist", 2, false}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "X", "Artist", []int{1, 2}, map[int]string{}}}},
		{"different prefixes remain leaves", []folder{{"Box/X CD1", "", "", 0, true}, {"Box/X CD2", "", "", 0, true}, {"Box/Y CD1", "", "", 0, true}}, nil, []wantCandidate{{CandidateBoxLeaf, "Box/X CD1", "X", "", []int{1}, map[int]string{}}, {CandidateBoxLeaf, "Box/X CD2", "X", "", []int{2}, map[int]string{}}, {CandidateBoxLeaf, "Box/Y CD1", "Y", "", []int{1}, map[int]string{}}}},
		{"plain numeric folders are ordinary", []folder{{"A/1", "", "", 0, false}, {"A/2", "", "", 0, false}}, nil, []wantCandidate{{CandidateOrdinary, "A/1", "1", "", []int{1}, map[int]string{}}, {CandidateOrdinary, "A/2", "2", "", []int{1}, map[int]string{}}}},
		{"root CD directories retain strict grouping", []folder{{"CD1", "Album", "Artist", 1, false}, {"CD2", "Album", "Artist", 2, false}}, nil, []wantCandidate{{CandidateMultiDisc, "", "Album", "Artist", []int{1, 2}, map[int]string{}}}},
		{"root album tags with disc markers", nil, []SourceObservation{{RelativePath: "X (Disc 1).flac", Album: "X (Disc 1)", DiscNumber: 1, InferredFields: map[string]bool{"disc_number": true}}, {RelativePath: "X (Disc 2).flac", Album: "X (Disc 2)", DiscNumber: 1, InferredFields: map[string]bool{"disc_number": true}}}, []wantCandidate{{CandidateLooseAlbum, "", "X", "", []int{1, 2}, map[int]string{}}}},
		{"exclusive unlabeled subtitle avoids directory artist fallback", []folder{{"A/Disc 1 - 夏", "", "", 0, true}, {"A/Disc 2 - 冬", "", "", 0, true}}, nil, []wantCandidate{{CandidateMultiDisc, "A", "A", "", []int{1, 2}, map[int]string{1: "夏", 2: "冬"}}}},
		{"nonexclusive Jay Chou prefixed leaf retains artist", []folder{{"周杰伦 - 叶惠美 CD1", "", "", 0, false}}, nil, []wantCandidate{{CandidateBoxLeaf, "周杰伦 - 叶惠美 CD1", "周杰伦 - 叶惠美", "周杰伦", []int{1}, map[int]string{}}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			observations := append([]SourceObservation(nil), tc.direct...)
			for _, dir := range tc.folders {
				observation := SourceObservation{RelativePath: dir.name + "/01.flac", Directory: dir.name, Album: dir.album, Artist: dir.artist, AlbumArtist: dir.artist, DiscNumber: dir.disc, DiscParentExclusive: dir.exclusive, InferredFields: map[string]bool{"disc_number": dir.disc == 0}}
				observations = append(observations, observation)
			}
			// 无标签时保留解析器给出的低置信度目录标题，不能伪造权威标签。
			for i := range observations {
				o := &observations[i]
				if o.Album == "" && o.Directory != "" {
					o.Album = path.Base(o.Directory)
					if o.InferredFields == nil {
						o.InferredFields = map[string]bool{}
					}
					o.InferredFields["album"] = true
				}
			}
			got := OrganizeObservations(observations)
			if len(got) != len(tc.want) {
				t.Fatalf("candidate count=%d want=%d; candidates=%#v", len(got), len(tc.want), got)
			}
			remaining := append([]OrganizedCandidate(nil), got...)
			for _, want := range tc.want {
				found := -1
				for i := range remaining {
					if remaining[i].Anchor.Kind == want.kind && remaining[i].Anchor.Scope == want.scope {
						found = i
						break
					}
				}
				if found < 0 {
					t.Errorf("missing %s candidate at %q; got %#v", want.kind, want.scope, got)
					continue
				}
				candidate := remaining[found]
				remaining = append(remaining[:found], remaining[found+1:]...)
				if candidate.Title != want.title || candidate.Artist != want.artist {
					t.Errorf("candidate %s/%q title/artist=%q/%q want %q/%q", want.kind, want.scope, candidate.Title, candidate.Artist, want.title, want.artist)
				}
				discs := make([]int, 0, len(candidate.Mediums))
				for disc := range candidate.Mediums {
					discs = append(discs, disc)
				}
				slices.Sort(discs)
				if !slices.Equal(discs, want.discs) {
					t.Errorf("candidate %s/%q discs=%v want=%v", want.kind, want.scope, discs, want.discs)
				}
				if !reflect.DeepEqual(candidate.MediumTitles, want.titles) {
					t.Errorf("candidate %s/%q medium titles=%#v want=%#v", want.kind, want.scope, candidate.MediumTitles, want.titles)
				}
				if tc.name == "exclusive unlabeled subtitle avoids directory artist fallback" {
					for _, decision := range candidate.Decisions {
						if (decision.Field == "artist" || decision.Field == "album_artist") && decision.Source == "folder" {
							t.Errorf("碟名产生了目录艺人证据: %#v", decision)
						}
					}
				}
				// 每份合成输入恰有一轨；目录顺序与期望的介质位置一一对应。
				if want.kind == CandidateMultiDisc && len(tc.folders) == len(want.discs) {
					for i, folder := range tc.folders {
						tracks := candidate.Mediums[want.discs[i]]
						if len(tracks) != 1 || tracks[0].Observation.RelativePath != folder.name+"/01.flac" || tracks[0].Disc != want.discs[i] {
							t.Errorf("目录 %q 的介质分配=%#v，期望 %d", folder.name, tracks, want.discs[i])
						}
					}
				}
			}
		})
	}
}

func TestOrganizeDiscNumberEvidencePriority(t *testing.T) {
	cases := []struct {
		name        string
		observation SourceObservation
		want        int
		source      string
	}{
		{"标签优先于专辑名和目录", SourceObservation{RelativePath: "A/CD3/01.flac", Album: "X (Disc 4)", DiscNumber: 2, FieldSources: map[string]string{"disc_number": "tag"}}, 2, "tag"},
		{"专辑名优先于目录", SourceObservation{RelativePath: "A/CD3/01.flac", Album: "X (Disc 2)", DiscNumber: 1, InferredFields: map[string]bool{"disc_number": true}}, 2, "album_title"},
		{"目录优先于默认值", SourceObservation{RelativePath: "A/CD3/01.flac", DiscNumber: 1, InferredFields: map[string]bool{"disc_number": true}}, 3, "folder_structure"},
		{"没有证据时默认一", SourceObservation{RelativePath: "A/01.flac", DiscNumber: 1, InferredFields: map[string]bool{"disc_number": true}, FieldSources: map[string]string{"disc_number": "default_fallback"}}, 1, "default_fallback"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := OrganizeObservations([]SourceObservation{tc.observation})
			if len(got) != 1 || len(got[0].Mediums[tc.want]) != 1 {
				t.Fatalf("expected disc %d: %#v", tc.want, got)
			}
			track := got[0].Mediums[tc.want][0].Observation
			if track.DiscNumber != tc.want || track.FieldSources["disc_number"] != tc.source {
				t.Fatalf("disc evidence=%d/%q want=%d/%q", track.DiscNumber, track.FieldSources["disc_number"], tc.want, tc.source)
			}
		})
	}
}
