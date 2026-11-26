package program

import (
	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
)

// IdentityDigest 与待定项结清时使用同一份识别依据。
func IdentityDigest(e catalog.CurationEvidence) string { return curation.ReleaseIdentityDigest(e) }

// GapDigest 只记录缺口与字段占用，供本地补全使用。
func GapDigest(e catalog.CurationEvidence) string {
	type trackGap struct {
		ID                                                               string
		TitleMissing, ArtistMissing, LocalLyrics, ExternalLyrics         bool
		TitleCorrected, ArtistCorrected, TitleAutomatic, ArtistAutomatic bool
	}
	gaps := struct {
		GapOrder                                                        [4]int
		Title, Artist, AlbumArtist, Year, Label, CatalogNumber, Barcode bool
		LocalArtwork, ExternalArtwork, ArtworkFile                      bool
		Corrected, Automatic                                            map[string]bool
		Tracks                                                          []trackGap
		Markers                                                         []catalog.CurationSuppression
	}{
		GapOrder: e.GapOrder(), Title: e.Authenticity["title"] == metadata.Inferred || e.TitlePlaceholder || e.Title == "", Artist: e.SelectedArtistAuth() == metadata.Inferred || e.ArtistPlaceholder || albumArtist(e) == "",
		AlbumArtist: e.AlbumArtist == "", Year: e.Year == 0, Label: e.Label == "",
		CatalogNumber: e.CatalogNumber == "" && !e.CatalogNumberIsMCN, Barcode: len(e.Barcodes) == 0,
		LocalArtwork: e.LocalArtwork, ExternalArtwork: e.ExternalArtwork, ArtworkFile: e.ArtworkFile,
		Corrected: e.Corrected, Automatic: e.Automatic,
	}
	for _, t := range e.Tracks {
		gaps.Tracks = append(gaps.Tracks, trackGap{
			ID: t.ID, TitleMissing: t.Title == "" || t.TitlePlaceholder || t.TitleInferred, ArtistMissing: t.Artist == "" || t.ArtistPlaceholder,
			LocalLyrics: t.LocalLyrics, ExternalLyrics: t.ExternalLyrics,
			TitleCorrected: t.TitleCorrected, ArtistCorrected: t.ArtistCorrected, TitleAutomatic: t.TitleAutomatic, ArtistAutomatic: t.ArtistAutomatic,
		})
	}
	for _, marker := range e.Suppressions {
		switch marker.Subject {
		case "title", "artist", "album_artist", "year", "catalog_number", "barcode", "label":
			gaps.Markers = append(gaps.Markers, marker)
		}
	}
	return digest(gaps)
}
