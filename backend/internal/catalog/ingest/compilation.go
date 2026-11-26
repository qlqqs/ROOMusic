package ingest

import (
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// applyCompilation 按候选实际曲目统计艺人分布与合辑标志。
func applyCompilation(observations []SourceObservation, artist, albumArtist *FieldDecision) {
	taggedAlbumArtist := false
	counts := map[string]int{}
	highest := 0
	compilations := 0
	for _, observation := range observations {
		taggedAlbumArtist = taggedAlbumArtist || observation.AlbumArtist != "" && !observation.InferredFields["album_artist"]
		if metadata.FieldAuthenticity("artist", observation.Artist, observation.FieldSources["artist"], observation.InferredFields["artist"], false) == metadata.Real {
			seen := map[string]bool{}
			for _, name := range metadata.CanonicalArtistNames(observation.Artist) {
				key := strings.ToLower(name)
				if seen[key] || metadata.IsCompilationPlaceholder(name) || metadata.FieldAuthenticity("artist", name, "", false, false) == metadata.Placeholder {
					continue
				}
				seen[key] = true
				counts[key]++
				highest = max(highest, counts[key])
			}
		}
		if observation.Compilation {
			compilations++
		}
	}
	if !taggedAlbumArtist && len(observations) >= 4 && len(counts) >= 3 && highest*2 <= len(observations) {
		*albumArtist = FieldDecision{Field: "album_artist", Value: "Various Artists", Source: "artist_distribution", Confidence: "low", Action: "uncertain_apply", RuleID: "compilation_distribution_v1", Reason: "inferred_value", Candidates: []string{"Various Artists"}}
		*artist = *albumArtist
		artist.Field = "artist"
		return
	}
	if albumArtist.Value != "" && !metadata.IsCompilationPlaceholder(albumArtist.Value) {
		return
	}
	if !metadata.IsCompilationPlaceholder(artist.Value) && compilations*2 <= len(observations) {
		return
	}
	*artist = FieldDecision{
		Field: "artist", Value: metadata.CompilationDisplayName, Source: "compilation",
		Confidence: "high", Action: "auto_apply", RuleID: "compilation_v1", Candidates: artist.Candidates,
	}
	*albumArtist = FieldDecision{}
}
