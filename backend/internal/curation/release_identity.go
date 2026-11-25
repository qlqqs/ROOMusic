package curation

import (
	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
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

// 升级现有工作状态时仍用 101 的格式核对基线。
func ReleaseIdentityDigest101(e catalog.CurationEvidence) string {
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
	return operations.Fingerprint(input)
}

// ReleaseIdentityDigest 覆盖文字、作品和当前外部对应等实际识别依据。
func ReleaseIdentityDigest(e catalog.CurationEvidence) string {
	type trackText struct {
		Title, Artist identityField
		Work          string
	}
	input := struct {
		Previous, GroupMatch, ArtistMBID string
		Classical, Compilation           bool
		Tracks                           []trackText
		Markers                          []catalog.CurationSuppression
	}{Previous: ReleaseIdentityDigest101(e), GroupMatch: e.ReleaseGroupMatchID, ArtistMBID: e.ArtistMBID, Classical: e.Classical, Compilation: e.Compilation}
	for _, t := range e.Tracks {
		input.Tracks = append(input.Tracks, trackText{
			Title:  identityField{t.Title, t.TitleObservation.Source, t.TitleObservation.Inferred, t.TitleCorrected},
			Artist: identityField{t.Artist, t.ArtistObservation.Source, t.ArtistObservation.Inferred, t.ArtistCorrected}, Work: t.WorkTitle})
	}
	for _, m := range e.Suppressions {
		if m.Subject == "external_link" && (m.TargetType == "release_group_match" || m.TargetType == "release_group") {
			input.Markers = append(input.Markers, m)
		}
	}
	return operations.Fingerprint(input)
}
