package catalog

import (
	"reflect"
	"testing"
)

func organizeSingleCandidate(t *testing.T, observations ...SourceObservation) OrganizedCandidate {
	t.Helper()
	candidates := OrganizeObservations(observations)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %d, want 1: %#v", len(candidates), candidates)
	}
	return candidates[0]
}

func TestArtworkDirectoriesFollowReleaseDirectoryThenTracks(t *testing.T) {
	// 与扫描器一致：虚拟路径是 sheet 相对路径 + "#track-N@" + 注册根相对的父音频路径 + "#index-M"。
	cueObservation := func(sheet, parent, directory string, track int) SourceObservation {
		return SourceObservation{
			RelativePath: sheet + "#track-" + string(rune('0'+track)) + "@" + parent + "#index-0", Directory: directory,
			Album: "Release", Artist: "Artist", SourceKind: "cue_virtual", TrackNumber: track, DiscNumber: 1,
			CueSheetPath: sheet, CueParentRelativePath: parent,
		}
	}
	cases := []struct {
		name         string
		observations []SourceObservation
		kind         CandidateKind
		want         []string
	}{
		{
			name: "ordinary directory",
			observations: []SourceObservation{
				{RelativePath: "A/01.flac", Directory: "A", Album: "Release", TrackNumber: 1, DiscNumber: 1},
				{RelativePath: "A/02.flac", Directory: "A", Album: "Release", TrackNumber: 2, DiscNumber: 1},
			},
			kind: CandidateOrdinary,
			want: []string{"A"},
		},
		{
			// 虚拟路径 A/album.cue#track-1@A/image.flac#index-0 的 filepath.Dir 是不存在的目录；必须使用观察的 Directory。
			name:         "cue uses sheet directory",
			observations: []SourceObservation{cueObservation("A/album.cue", "A/image.flac", "A", 1), cueObservation("A/album.cue", "A/image.flac", "A", 2)},
			kind:         CandidateOrdinary,
			want:         []string{"A"},
		},
		{
			name: "strict multi-disc searches parent before disc directories",
			observations: []SourceObservation{
				{RelativePath: "A/CD1/01.flac", Directory: "A/CD1", Album: "Release", Artist: "Artist", DiscNumber: 1, TrackNumber: 1},
				{RelativePath: "A/CD2/01.flac", Directory: "A/CD2", Album: "Release", Artist: "Artist", DiscNumber: 2, TrackNumber: 1},
			},
			kind: CandidateMultiDisc,
			want: []string{"A", "A/CD1", "A/CD2"},
		},
		{
			name: "disc ten sorts after disc two",
			observations: func() []SourceObservation {
				var observations []SourceObservation
				for disc := 1; disc <= 10; disc++ {
					directory := "A/CD" + string(rune('0'+disc%10))
					if disc == 10 {
						directory = "A/CD10"
					}
					observations = append(observations, SourceObservation{RelativePath: directory + "/01.flac", Directory: directory, Album: "Release", Artist: "Artist", DiscNumber: disc, TrackNumber: 1})
				}
				return observations
			}(),
			kind: CandidateMultiDisc,
			want: []string{"A", "A/CD1", "A/CD2", "A/CD3", "A/CD4", "A/CD5", "A/CD6", "A/CD7", "A/CD8", "A/CD9", "A/CD10"},
		},
		{
			// 非连续碟号不合并，Box 叶子只查自己的目录，不查 Box 父目录。
			name: "box leaf stays in its own directory",
			observations: []SourceObservation{
				{RelativePath: "Box/Disc 1/a.flac", Directory: "Box/Disc 1", Album: "Set", DiscNumber: 1, TrackNumber: 1},
				{RelativePath: "Box/Disc 3/b.flac", Directory: "Box/Disc 3", Album: "Set", DiscNumber: 3, TrackNumber: 1},
			},
			kind: CandidateBoxLeaf,
			want: []string{"Box/Disc 1"},
		},
		{
			name: "loose files at the registered root",
			observations: []SourceObservation{
				{RelativePath: "a.flac", Album: "Release", TrackNumber: 1},
				{RelativePath: "b.flac", Album: "Release", TrackNumber: 2},
			},
			kind: CandidateLooseAlbum,
			want: []string{""},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			candidates := OrganizeObservations(test.observations)
			if test.kind == CandidateBoxLeaf {
				// 两个 Box 叶子各成一个候选，取碟 1 那个。
				if len(candidates) != 2 {
					t.Fatalf("candidates = %d, want 2", len(candidates))
				}
				candidates = candidates[:1]
			}
			if len(candidates) != 1 || candidates[0].Anchor.Kind != test.kind {
				t.Fatalf("unexpected candidates: %#v", candidates)
			}
			if got := candidates[0].ArtworkDirectories(); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ArtworkDirectories = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestArtworkDirectoriesDoNotLeaveCrossDirectoryCueSheetDirectory(t *testing.T) {
	observation := func(track int) SourceObservation {
		return SourceObservation{
			RelativePath: "sheets/album.cue#track-" + string(rune('0'+track)) + "@media/image.flac#index-0", Directory: "sheets",
			Album: "Release", Artist: "Artist", SourceKind: "cue_virtual", TrackNumber: track, DiscNumber: 1,
			CueSheetPath: "sheets/album.cue", CueParentRelativePath: "media/image.flac",
		}
	}
	candidate := organizeSingleCandidate(t, observation(1), observation(2))
	if got, want := candidate.ArtworkDirectories(), []string{"sheets"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ArtworkDirectories = %#v, want %#v", got, want)
	}
}

func TestArtworkDirectoriesDeduplicateAndOrderTracksByPosition(t *testing.T) {
	candidate := OrganizedCandidate{
		Anchor: CandidateAnchor{Kind: CandidateOrdinary, Scope: "A"},
		Mediums: map[int][]OrganizedTrack{
			2: {{Observation: SourceObservation{Directory: "A/CD2"}, Position: 1}},
			1: {
				{Observation: SourceObservation{Directory: "A/Bonus"}, Position: 2},
				{Observation: SourceObservation{Directory: "A/CD1"}, Position: 1},
				{Observation: SourceObservation{Directory: "A/CD1"}, Position: 3},
			},
		},
	}
	if got, want := candidate.ArtworkDirectories(), []string{"A", "A/CD1", "A/Bonus", "A/CD2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ArtworkDirectories = %#v, want %#v", got, want)
	}
	// 调用不应改动候选自身的曲目顺序。
	if candidate.Mediums[1][0].Position != 2 {
		t.Fatalf("ArtworkDirectories reordered the candidate: %#v", candidate.Mediums[1])
	}
}

func TestEmbeddedArtworkPicksFirstTrackInDiscThenPositionOrder(t *testing.T) {
	artwork := func(marker byte) []byte { return []byte{0x89, 'P', 'N', 'G', marker} }
	candidate := OrganizedCandidate{
		Mediums: map[int][]OrganizedTrack{
			2: {{Observation: SourceObservation{RelativePath: "d2", Artwork: artwork(2)}, Position: 1}},
			1: {
				{Observation: SourceObservation{RelativePath: "d1-2", Artwork: artwork(12)}, Position: 2},
				{Observation: SourceObservation{RelativePath: "d1-1"}, Position: 1},
			},
		},
	}
	observation, ok := candidate.EmbeddedArtwork()
	if !ok || observation.RelativePath != "d1-2" {
		t.Fatalf("EmbeddedArtwork = %q, %v; want d1-2", observation.RelativePath, ok)
	}

	candidate.Mediums[1][0].Observation.Artwork = nil
	observation, ok = candidate.EmbeddedArtwork()
	if !ok || observation.RelativePath != "d2" {
		t.Fatalf("EmbeddedArtwork = %q, %v; want d2", observation.RelativePath, ok)
	}

	candidate.Mediums[2][0].Observation.Artwork = nil
	if _, ok := candidate.EmbeddedArtwork(); ok {
		t.Fatal("EmbeddedArtwork reported artwork for tracks without any")
	}
}
