package ingest

import "github.com/qlqq/roomusic/backend/internal/catalog/metadata"

// applyCompilation 按候选实际曲目统计艺人分布与合辑标志。
func applyCompilation(observations []SourceObservation, artist, albumArtist *FieldDecision) {
	taggedAlbumArtist := false
	counts := metadata.PrimaryArtistCounts{}
	compilations := 0
	for _, observation := range observations {
		taggedAlbumArtist = taggedAlbumArtist || observation.AlbumArtist != "" && !observation.InferredFields["album_artist"]
		if metadata.FieldAuthenticity("artist", observation.Artist, observation.FieldSources["artist"], observation.InferredFields["artist"], false) == metadata.Real {
			counts.Add(firstTrackArtistCredit(observation))
		}
		if observation.Compilation {
			compilations++
		}
	}
	if !taggedAlbumArtist && counts.IsCompilation(len(observations)) {
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

// 展示串已按名字排序。原始署名按出现序号和标签键取首项，与整理读取的次序一致。
func firstTrackArtistCredit(o SourceObservation) string {
	var first *metadata.ArtistTagEvidence
	for i := range o.ArtistTagEvidence {
		tag := &o.ArtistTagEvidence[i]
		if tag.Role != "track_artist" {
			continue
		}
		if first == nil || tag.OccurrenceIndex < first.OccurrenceIndex || tag.OccurrenceIndex == first.OccurrenceIndex && tag.TagKey < first.TagKey {
			first = tag
		}
	}
	if first != nil {
		return first.RawValue
	}
	return o.Artist
}
