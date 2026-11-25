package program

import (
	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

type identityField struct {
	Value, Source       string
	Inferred, Corrected bool
}

type cueInput struct {
	RootID, Sheet, Parent string
	Index, End            *int
}

type identityTrack struct {
	ID, ExternalID           string
	Disc, Position, Duration int
	PositionObservation      catalog.CurationObservation
	Title, Artist            identityField
	Identifiers              map[string][]string
	CueISRC                  string
	Cue                      *cueInput
}

// IdentityDigest 只记录查询与核对所用的本地原始事实。
func IdentityDigest(e catalog.CurationEvidence) string {
	type releaseInput struct {
		Kind, ExternalID                                        string
		Title, Artist, Barcode, CatalogNumber, Label, MediaType identityField
		Media                                                   []int
		Tracks                                                  []identityTrack
		Markers                                                 []catalog.CurationSuppression
	}
	field := func(name, value string) identityField {
		return identityField{Value: value, Source: e.FieldSources[name], Corrected: e.Corrected[name]}
	}
	single := e.CandidateKind == "loose_unknown" && len(e.Tracks) == 1
	input := releaseInput{Kind: e.CandidateKind, ExternalID: e.ExternalID, Media: e.Media}
	if !single {
		input.Title = field("title", e.Title)
		input.Artist = field("artist", e.Artist)
		if e.AlbumArtist != "" {
			input.Artist = field("album_artist", e.AlbumArtist)
		}
	}
	// 信息不足的判断也检查条码、目录号和碟片证据。
	input.Barcode = field("barcode", e.Barcode)
	number := e.RawCatalogNumber
	if number == "" {
		number = e.CatalogNumber
	}
	input.CatalogNumber = field("catalog_number", number)
	input.Label = field("label", e.Label)
	input.MediaType = field("media_type", e.MediaType)
	for _, t := range e.Tracks {
		row := identityTrack{ID: t.ID, ExternalID: t.ExternalID, Disc: t.Disc, Position: t.Position,
			PositionObservation: t.PositionObservation, Duration: t.DurationSeconds, Identifiers: t.Identifiers, CueISRC: t.CueISRC}
		if t.SourceKind == "cue_virtual" {
			row.Cue = &cueInput{t.SourceRootID, t.CueSheetPath, t.ParentFile, t.CueIndexFrames, t.CueEndFrames}
		}
		if single {
			row.Title = identityField{t.Title, t.TitleObservation.Source, t.TitleObservation.Inferred, t.TitleCorrected}
			row.Artist = identityField{t.Artist, t.ArtistObservation.Source, t.ArtistObservation.Inferred, t.ArtistCorrected}
		}
		input.Tracks = append(input.Tracks, row)
	}
	for _, marker := range e.Suppressions {
		if marker.Subject == "external_link" && (marker.TargetType == "release" || marker.TargetType == "track") {
			input.Markers = append(input.Markers, marker)
		}
	}
	return digest(input)
}

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
