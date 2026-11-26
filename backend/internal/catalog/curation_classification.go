package catalog

import (
	"context"
	"database/sql"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
)

type CurationClass string

const (
	TaggedID      CurationClass = "tagged_id"
	Insufficient  CurationClass = "insufficient"
	HasGaps       CurationClass = "has_gaps"
	LocalComplete CurationClass = "local_complete"
)

type CurationClassification struct {
	Class CurationClass
	// 按标题及艺人、曲名、本地封面、年份的缺口数逐项降序排列。
	GapOrder [4]int
}

func (e CurationEvidence) SearchBarcodes() []string {
	var result []string
	for i, v := range e.Barcodes {
		if i >= len(e.BarcodeAuthenticity) || e.BarcodeAuthenticity[i] != metadata.Placeholder {
			result = append(result, v)
		}
	}
	return result
}

func (e CurationEvidence) effectiveIdentityAuth() (metadata.Authenticity, metadata.Authenticity) {
	if e.CandidateKind == "loose_unknown" && len(e.Tracks) == 1 {
		return e.Tracks[0].EffectiveTitleAuth, e.Tracks[0].EffectiveArtistAuth
	}
	return e.EffectiveTitleAuth, e.EffectiveArtistAuth
}

func (e CurationEvidence) GapOrder() [4]int {
	var order [4]int
	title, artist := e.effectiveIdentityAuth()
	for _, a := range []metadata.Authenticity{title, artist} {
		if a != metadata.Real {
			order[0]++
		}
	}
	for _, t := range e.Tracks {
		if t.EffectiveTitleAuth != metadata.Real {
			order[1]++
		}
	}
	if !e.LocalArtwork {
		order[2] = 1
	}
	if e.EffectiveYear == 0 {
		order[3] = 1
	}
	return order
}

// HasSearchID 区分带外部编号的发行与仅靠文字、曲目结构识别的发行。
func (e CurationEvidence) HasSearchID() bool {
	if e.ExternalID != "" || e.GroupExternalID != "" || e.ExternalGroupID != "" ||
		len(e.SearchBarcodes()) > 0 || e.Authenticity["catalog_number"].SearchValue(e.CatalogNumber) != "" {
		return true
	}
	for _, t := range e.Tracks {
		if t.OtherIdentifier || t.ExternalID != "" || t.CueISRC != "" {
			return true
		}
		for _, ids := range t.Identifiers {
			if len(ids) > 0 {
				return true
			}
		}
	}
	return false
}

func (e CurationEvidence) Classification() CurationClassification {
	c := CurationClassification{GapOrder: e.GapOrder()}
	title, artist := e.effectiveIdentityAuth()
	single := e.CandidateKind == "loose_unknown"
	tag := "mb_release"
	if single {
		tag = "mb_recording"
	}
	tagged := false
	for _, t := range e.Tracks {
		if (!single || len(e.Tracks) == 1) && slices.ContainsFunc(t.Identifiers[tag], func(id string) bool { return id != "" }) {
			tagged = true
			break
		}
	}
	switch {
	case tagged:
		c.Class = TaggedID
	case title == metadata.Placeholder && artist == metadata.Placeholder && !e.HasSearchID() && len(e.DiscTOCs()) == 0:
		c.Class = Insufficient
	case c.GapOrder != [4]int{}:
		c.Class = HasGaps
	default:
		c.Class = LocalComplete
	}
	return c
}

// 可检索的两项名字决定文字准入；两项都缺时只允许声纹。
func (e CurationEvidence) CanMatch(title, artist string, hasID, fingerprints bool) bool {
	if hasID {
		return true
	}
	if e.CandidateKind == "loose_unknown" && len(e.Tracks) != 1 {
		return false
	}
	hasArtist := artist != "" || e.Compilation && e.CandidateKind != "loose_unknown"
	return title != "" && hasArtist || title == "" && !hasArtist && fingerprints
}

type CurationClassCounts struct {
	Total         int `json:"total"`
	TaggedID      int `json:"tagged_id"`
	HasGaps       int `json:"has_gaps"`
	LocalComplete int `json:"local_complete"`
	Insufficient  int `json:"insufficient"`
}

func CountCurationClasses(ctx context.Context, db ReadTransactionBeginner) (CurationClassCounts, error) {
	return readWithLocalJITOff(ctx, db, func(tx *sql.Tx) (CurationClassCounts, error) {
		var counts CurationClassCounts
		var after *CurationCursor
		for {
			page, err := PageCurationReleases(ctx, tx, after, 100)
			if err != nil || len(page) == 0 {
				return counts, err
			}
			ids := make([]string, len(page))
			for i, row := range page {
				ids[i] = row.ID
			}
			entries, err := curationHeaders(ctx, tx, ids, nil)
			if err != nil {
				return counts, err
			}
			if err := loadCurationTracks(ctx, tx, ids, entries, false); err != nil {
				return counts, err
			}
			for _, e := range entries {
				counts.Total++
				switch e.Classification().Class {
				case TaggedID:
					counts.TaggedID++
				case HasGaps:
					counts.HasGaps++
				case LocalComplete:
					counts.LocalComplete++
				case Insufficient:
					counts.Insufficient++
				}
			}
			after = &page[len(page)-1]
		}
	})
}
