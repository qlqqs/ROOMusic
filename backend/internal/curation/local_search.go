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
			e.Authenticity["title"], e.FieldSources["title"] = metadata.Inferred, e.Legacy.LocalValueSource("title", e.LocalTitle)
		}
		if e.LocalArtist != "" {
			e.AlbumArtist, e.ArtistPlaceholder = e.LocalArtist, false
			e.Authenticity["album_artist"], e.FieldSources["album_artist"] = metadata.Inferred, e.Legacy.LocalValueSource("album_artist", e.LocalArtist)
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
			track.TitleObservation = catalog.CurationObservation{Source: track.Legacy.LocalValueSource("title", track.LocalTitle), Inferred: true}
		}
		if track.LocalArtist != "" {
			track.Artist, track.ArtistPlaceholder = track.LocalArtist, false
			track.Authenticity["artist"] = metadata.Inferred
			track.ArtistObservation = catalog.CurationObservation{Source: track.Legacy.LocalValueSource("artist", track.LocalArtist), Inferred: true}
		}
		e.Tracks[i] = track
	}
	return e
}

// CompilationEvidence 按原始曲目署名判别无专辑艺人的合辑，显式合辑标记优先。
func CompilationEvidence(e catalog.CurationEvidence) bool {
	hasAlbumArtist := e.AlbumArtist != "" && e.Authenticity["album_artist"] != metadata.Placeholder && e.Authenticity["album_artist"] != metadata.Inferred
	albumMarker := e.FieldSources["album_artist"] != "artist_distribution" && metadata.IsCompilationPlaceholder(e.AlbumArtist)
	artistMarker := e.FieldSources["artist"] != "artist_distribution" && metadata.IsCompilationPlaceholder(e.Artist)
	if albumMarker || !hasAlbumArtist && (e.FieldSources["artist"] == "compilation" || artistMarker) {
		return true
	}
	if hasAlbumArtist || len(e.Tracks) < 4 {
		return false
	}
	counts := metadata.PrimaryArtistCounts{}
	for _, track := range e.Tracks {
		if track.ArtistPlaceholder || track.Authenticity["artist"] == metadata.Placeholder || track.Authenticity["artist"] == metadata.Inferred {
			continue
		}
		credit := track.Artist
		if !track.ArtistCorrected && track.FirstArtistCredit != "" {
			credit = track.FirstArtistCredit
		}
		counts.Add(credit)
	}
	return counts.IsCompilation(len(e.Tracks))
}
