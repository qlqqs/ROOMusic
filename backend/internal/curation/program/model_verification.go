package program

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
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

func verifyModelRecording(e catalog.CurationEvidence, r sources.Recording) *MatchVerification {
	if _, within := compareLength(e.Tracks[0].DurationSeconds, r.LengthMS); within {
		e = curation.LocalSearchEvidence(e)
	}
	return verifyRecording(e, r)
}

func verifyRecording(e catalog.CurationEvidence, r sources.Recording) *MatchVerification {
	t := e.Tracks[0]
	v := &MatchVerification{Method: "independent_review", LocalTracks: 1, ExternalTracks: 1, TitleMatch: !t.TitlePlaceholder && sameText(t.Title, r.Title), ArtistMatch: !t.ArtistPlaceholder && sameArtist(t.Artist, credits(r.Artists))}
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

// 只有文字会成为反证时才读取艺人条目。合署名按外部位置核对，每个名字都要能对应。
func tagArtistNamesAgree(ctx context.Context, r sources.Release, local, canonical string, finder sources.ArtistFinder, known map[string]sources.Artist) (bool, error) {
	for _, name := range []string{local, canonical} {
		if sameArtist(name, credits(r.Artists)) {
			return true, nil
		}
	}
	for _, name := range []string{local, canonical} {
		names := artistNameKeys(name)
		if len(r.Artists) == 1 && name != "" {
			names = []string{artistKey(name)}
		}
		if len(names) == 0 || len(names) != len(r.Artists) {
			continue
		}
		match := true
		for i, credit := range r.Artists {
			if artistKey(credit.Name) == names[i] {
				continue
			}
			a, ok := known[credit.ArtistID]
			if !ok && finder != nil && credit.ArtistID != "" {
				var err error
				a, err = found(finder.ArtistByID(ctx, credit.ArtistID))
				if err != nil {
					return false, err
				}
				known[credit.ArtistID] = a
			}
			if a.ID == "" || a.ID != credit.ArtistID || !registeredArtistKey(a, names[i]) {
				match = false
				break
			}
		}
		if match {
			return true, nil
		}
	}
	return false, nil
}

func registeredArtistKey(a sources.Artist, key string) bool {
	if key == "" {
		return false
	}
	return artistKey(a.Name) == key || slices.ContainsFunc(a.Aliases, func(alias sources.Alias) bool {
		return artistKey(alias.Name) == key
	})
}

func (m *ModelMatcher) verifyAlbum(ctx context.Context, e catalog.CurationEvidence, r sources.Release, c comparison) (*MatchVerification, error) {
	v := verifyAlbum(e, r, c)
	if !slices.Contains(v.Conflicts, "tag_text_mismatch") {
		return v, nil
	}
	return m.verifyArtistText(ctx, e, r, c, "", map[string]sources.Artist{})
}

func (m *ModelMatcher) verifyArtistText(ctx context.Context, e catalog.CurationEvidence, r sources.Release, c comparison, canonical string, known map[string]sources.Artist) (*MatchVerification, error) {
	finder, _ := m.sources.ArtistFinder("musicbrainz")
	matches, err := tagArtistNamesAgree(ctx, r, albumArtist(e), canonical, finder, known)
	if err != nil {
		return nil, err
	}
	c.artistMatch = matches
	return verifyAlbum(e, r, c), nil
}

func (m *ModelMatcher) selectionEvidence(ctx context.Context, item *curation.PendingItem, candidates []Candidate) error {
	if len(candidates) == 0 {
		return nil
	}
	e, err := m.evidence(ctx, m.db, item.TargetID)
	if err != nil || ReleaseDigest(e) != item.InputDigest {
		return err
	}
	e.Compilation = curation.CompilationEvidence(e)
	artists := map[string]sources.Artist{}
	for i := range candidates {
		row := &candidates[i]
		if row.Recording != nil && len(e.Tracks) == 1 {
			row.Verification = verifyModelRecording(e, *row.Recording)
		}
		if row.Release == nil {
			continue
		}
		var c comparison
		c.compareRelease(e, "model", *row.Release)
		c.compareTracks(e, "model", *row.Release)
		if !c.countEqual {
			continue
		}
		row.Verification = verifyAlbum(e, *row.Release, c)
		if slices.Contains(row.Verification.Conflicts, "tag_text_mismatch") {
			row.Verification, err = m.verifyArtistText(ctx, e, *row.Release, c, item.CanonicalArtist, artists)
			if err != nil {
				return err
			}
		}
	}
	slices.SortStableFunc(candidates, func(a, b Candidate) int {
		aConflict := a.Verification != nil && a.Verification.Contradicted
		bConflict := b.Verification != nil && b.Verification.Contradicted
		if aConflict == bConflict {
			return 0
		}
		if aConflict {
			return 1
		}
		return -1
	})
	if len(artists) != 0 {
		var evidence map[string]json.RawMessage
		if err := json.Unmarshal(item.Evidence, &evidence); err != nil {
			return err
		}
		evidence["artist_details"] = jsonValue(artists)
		item.Evidence = jsonValue(evidence)
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
