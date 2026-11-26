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
	_, canonical, err := m.writer.CanonicalPatrolNames(ctx, e.ID, "")
	if err != nil {
		return nil, err
	}
	return m.verifyArtistText(ctx, e, r, c, canonical, map[string]sources.Artist{})
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

func (m *ModelMatcher) AddCanonicalNames(ctx context.Context, items []curation.PendingItem) error {
	for i := range items {
		p := &items[i]
		switch p.Kind {
		case "release_match", "recording_match", "verify_name":
			var input struct {
				Tracks  []struct{ ID string }
				TrackID string `json:"track_id"`
			}
			if err := json.Unmarshal(p.Evidence, &input); err != nil {
				return err
			}
			trackID := input.TrackID
			if p.Kind == "recording_match" && len(input.Tracks) == 1 {
				trackID = input.Tracks[0].ID
			}
			title, artist, err := m.writer.CanonicalPatrolNames(ctx, p.TargetID, trackID)
			if err != nil {
				return err
			}
			p.CanonicalTitle, p.CanonicalArtist = title, artist
		}
	}
	return nil
}

// PrepareSelection 补齐可选编号的曲目清单。来源失败交给调度退避，已失效的编号不进入选择题。
func (m *ModelMatcher) PrepareSelection(ctx context.Context, items []curation.PendingItem) error {
	if err := m.AnnotatePendingEvidence(ctx, items); err != nil {
		return err
	}
	if err := m.AddCanonicalNames(ctx, items); err != nil {
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
		if err := m.selectionEvidence(ctx, item, available); err != nil {
			return err
		}
		item.Candidates = jsonValue(available)
	}
	return nil
}

// 第一档没有读取工具。把实际核对出的反证和使用过的艺人资料附给模型，反证候选排在其余候选之后。
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
