package program

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type DurationDifference struct {
	Disc         int   `json:"disc"`
	Position     int   `json:"position"`
	DifferenceMS int64 `json:"difference_ms"`
}

type MatchVerification struct {
	Method                string               `json:"method"`
	Direct                bool                 `json:"direct"`
	Contradicted          bool                 `json:"contradicted"`
	LocalTracks           int                  `json:"local_tracks"`
	ExternalTracks        int                  `json:"external_tracks"`
	Comparable            int                  `json:"comparable"`
	Within                int                  `json:"within"`
	TitleMatch            bool                 `json:"title_match"`
	ArtistMatch           bool                 `json:"artist_match"`
	MatchedTrackTitles    int                  `json:"matched_track_titles"`
	ComparableTrackTitles int                  `json:"comparable_track_titles"`
	Differences           []DurationDifference `json:"differences,omitempty"`
	Conflicts             []string             `json:"conflicts,omitempty"`
}

func verifyRecording(e catalog.CurationEvidence, r sources.Recording) *MatchVerification {
	t := e.Tracks[0]
	v := &MatchVerification{Method: "independent_review", LocalTracks: 1, ExternalTracks: 1, TitleMatch: !t.TitlePlaceholder && sameText(t.Title, r.Title), ArtistMatch: !t.ArtistPlaceholder && sameText(t.Artist, credits(r.Artists))}
	known, within := compareLength(t.DurationSeconds, r.LengthMS)
	if known {
		v.Comparable = 1
	}
	if within {
		v.Within = 1
	} else if known {
		v.Differences = []DurationDifference{{t.Disc, t.Position, int64(t.DurationSeconds)*1000 - *r.LengthMS}}
	}
	agree, bad := compareISRCs(trackISRCs(t), r.ISRCs)
	if bad {
		v.Conflicts = append(v.Conflicts, "isrc_mismatch")
	}
	ids := t.Identifiers["mb_recording"]
	if len(ids) > 0 && !slices.Contains(ids, r.ID) {
		v.Conflicts = append(v.Conflicts, "recording_tag_mismatch")
	}
	v.Direct = within && v.TitleMatch && v.ArtistMatch && len(v.Conflicts) == 0
	if v.Direct {
		v.Method = "recording_duration_text"
	}
	v.Contradicted = len(v.Conflicts) > 0 || !agree && ((known && !within) || (t.Authenticity["title"] == metadata.Real && t.Authenticity["artist"] == metadata.Real && !v.TitleMatch && !v.ArtistMatch))
	return v
}

func verifyAlbum(e catalog.CurationEvidence, r sources.Release, c comparison) *MatchVerification {
	v := &MatchVerification{Method: "independent_review", LocalTracks: len(e.Tracks), ExternalTracks: len(c.pairs), Comparable: c.comparable, Within: c.within, TitleMatch: c.titleMatch, ArtistMatch: c.artistMatch, MatchedTrackTitles: c.tagText.Matches, ComparableTrackTitles: c.tagText.Comparable, Differences: c.differences, Conflicts: slices.Clone(c.conflicts)}
	if e.Authenticity["title"] == metadata.Real && e.SelectedArtistAuth() == metadata.Real && !c.titleMatch && !c.artistMatch && c.tagText.Comparable > 0 && c.tagText.Matches == 0 {
		v.Conflicts = append(v.Conflicts, "tag_text_mismatch")
	}
	independentID := c.isrcAgree > 0 || slices.ContainsFunc(e.Barcodes, func(b string) bool { return barcodeKey(b) != "" && barcodeKey(b) == barcodeKey(r.Barcode) })
	for _, code := range v.Conflicts {
		if (code == "duration_mismatch" || code == "tag_text_mismatch" || code == "inferred_positions") && independentID {
			continue
		}
		v.Contradicted = true
	}
	v.Direct = len(v.Conflicts) == 0 && c.countEqual && len(e.Tracks) > 4 && c.comparable > 0 && c.comparable == c.within
	if v.Direct {
		v.Method = "album_structure"
	}
	return v
}

// PrepareSelection 补齐可选编号的曲目清单。来源失败交给调度退避，已失效的编号不进入选择题。
func (m *ModelMatcher) PrepareSelection(ctx context.Context, items []curation.PendingItem) error {
	if err := m.AnnotatePendingEvidence(ctx, items); err != nil {
		return err
	}
	for i := range items {
		item := &items[i]
		var rows []Candidate
		if err := json.Unmarshal(item.Candidates, &rows); err != nil {
			return err
		}
		available := make([]Candidate, 0, len(rows))
		for _, row := range rows {
			if !uuid.Valid(row.ExternalID) {
				continue
			}
			switch item.Kind {
			case "release_match":
				if !completeRelease(row.Release, row.ExternalID) || !row.Release.Complete {
					f, ok := m.sources.ReleaseFinder("musicbrainz")
					if !ok {
						return operations.Conflict("source_disabled", "发行查询未启用")
					}
					r, err := f.ReleaseByID(ctx, row.ExternalID)
					if errors.Is(err, sources.ErrNotFound) {
						continue
					}
					if err != nil {
						return err
					}
					row.Release = &r
				}
				if !completeRelease(row.Release, row.ExternalID) {
					continue
				}
			case "recording_match":
				if row.Recording == nil || row.Recording.ID != row.ExternalID {
					f, ok := m.sources.RecordingFinder("musicbrainz")
					if !ok {
						return operations.Conflict("source_disabled", "录音查询未启用")
					}
					r, err := f.RecordingByID(ctx, row.ExternalID)
					if errors.Is(err, sources.ErrNotFound) {
						continue
					}
					if err != nil {
						return err
					}
					if r.ID != row.ExternalID {
						continue
					}
					row.Recording = &r
				}
			default:
				return operations.Invalid("invalid_input", "选择项种类无效")
			}
			available = append(available, row)
		}
		item.Candidates = jsonValue(available)
	}
	return nil
}

func completeRelease(r *sources.Release, id string) bool {
	if r == nil || r.ID != id || len(r.Media) == 0 {
		return false
	}
	for _, disc := range r.Media {
		if len(disc.Tracks) == 0 || disc.TrackCount > 0 && len(disc.Tracks) != disc.TrackCount {
			return false
		}
	}
	return true
}
