package curation

import (
	"maps"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

// LocalSearchEvidence 只向匹配器提供仍有效的本地整理文字，原始依据继续用于写入与佐证。
func LocalSearchEvidence(e catalog.CurationEvidence) catalog.CurationEvidence {
	if e.LocalTitle != "" || e.LocalArtist != "" {
		e.Authenticity = maps.Clone(e.Authenticity)
		e.FieldSources = maps.Clone(e.FieldSources)
		if e.LocalTitle != "" {
			e.Title, e.TitlePlaceholder = e.LocalTitle, false
			e.Authenticity["title"], e.FieldSources["title"] = metadata.Inferred, "model_inferred"
		}
		if e.LocalArtist != "" {
			e.AlbumArtist, e.ArtistPlaceholder = e.LocalArtist, false
			e.Authenticity["album_artist"], e.FieldSources["album_artist"] = metadata.Inferred, "model_inferred"
		}
	}
	cloned := false
	for i, track := range e.Tracks {
		if track.LocalTitle == "" && track.LocalArtist == "" {
			continue
		}
		if !cloned {
			e.Tracks = slices.Clone(e.Tracks)
			cloned = true
		}
		track.Authenticity = maps.Clone(track.Authenticity)
		if track.LocalTitle != "" {
			track.Title, track.TitlePlaceholder, track.TitleInferred = track.LocalTitle, false, true
			track.Authenticity["title"] = metadata.Inferred
			track.TitleObservation = catalog.CurationObservation{Source: "model_inferred", Inferred: true}
		}
		if track.LocalArtist != "" {
			track.Artist, track.ArtistPlaceholder = track.LocalArtist, false
			track.Authenticity["artist"] = metadata.Inferred
			track.ArtistObservation = catalog.CurationObservation{Source: "model_inferred", Inferred: true}
		}
		e.Tracks[i] = track
	}
	return e
}
