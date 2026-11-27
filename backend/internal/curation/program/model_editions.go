package program

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// 模型给出的编号只是起点。先核对程序候选，尚不能确认多版时再读发行组。
func (m *ModelMatcher) checkModelEditions(ctx context.Context, e catalog.CurationEvidence, item curation.PendingItem, match MatchResult) (MatchResult, error) {
	if match.Rule == "partial_subset" || match.Rule == "partial_superset" {
		return m.checkModelPartialEditions(ctx, curation.LocalSearchEvidence(e), item, match)
	}
	selected := match.Release
	if selected == nil || match.Verification.Contradicted || selected.Group.ID == "" || e.ExternalID == selected.ID {
		return match, nil
	}
	releaseTags := tags(e.Tracks, "mb_release")
	q := curation.ReleaseSearch{
		Barcodes:      e.SearchBarcodes(),
		CatalogNumber: e.Authenticity["catalog_number"].SearchValue(e.CatalogNumber),
		Label:         e.Authenticity["label"].SearchValue(e.Label),
	}
	hits, _ := identifierHits(q, []sources.Release{*selected})
	if len(hits) > 0 || len(releaseTags) == 1 && releaseTags[0] == selected.ID {
		return match, nil
	}
	var candidates []Candidate
	if len(item.Candidates) > 0 {
		if err := json.Unmarshal(item.Candidates, &candidates); err != nil {
			return MatchResult{}, err
		}
	}
	finder, ok := m.sources.ReleaseFinder("musicbrainz")
	if !ok {
		return MatchResult{}, sources.ErrUnavailable
	}
	var c comparison
	c.compareTracks(e, "model", *selected)
	passed := []verifiedEdition{{selected, c}}
	seen := map[string]bool{selected.ID: true}
	check := func(id string, r *sources.Release) error {
		if seen[id] {
			return nil
		}
		seen[id] = true
		if !completeRelease(r, id) || !r.Complete {
			detail, err := found(finder.ReleaseByID(ctx, id))
			if err != nil || detail.ID == "" {
				return err
			}
			if detail.ID != id {
				return sources.ErrUnavailable
			}
			r = &detail
		}
		if r.Group.ID != selected.Group.ID || len(e.DiscPositions()) != len(r.Media) {
			return nil
		}
		var c comparison
		c.compareRelease(e, "model", *r)
		c.compareTracks(e, "model", *r)
		if match.Method == "search" {
			// 管家点名的版本与同组版本须通过相同的程序核对，短专辑也按标题、艺人和时长比较。
			other := decideAlbum(e, match.Method, []sources.Release{*r}, MatchSnapshot{})
			if other.Level == "release" && !other.Candidates[0].Verification.Contradicted && !slices.Contains(c.conflicts, "position_conflict") && !slices.ContainsFunc(c.pairs, func(p TrackPair) bool { return p.RecordingID == "" }) {
				passed = append(passed, verifiedEdition{r, c})
			}
			return nil
		}
		proof, err := m.verifyAlbum(ctx, e, *r, c)
		if err != nil {
			return err
		}
		if c.countEqual && !proof.Contradicted && c.within == c.comparable &&
			(!match.Verification.Direct || proof.Direct) && !slices.ContainsFunc(c.pairs, func(p TrackPair) bool { return p.RecordingID == "" }) {
			passed = append(passed, verifiedEdition{r, c})
		}
		return nil
	}
	counts := localDiscCounts(e)
	for _, row := range candidates {
		if row.ReleaseGroupID != selected.Group.ID && (row.Release == nil || row.Release.Group.ID != selected.Group.ID) {
			continue
		}
		if wrong, _ := searchShape(counts, len(e.Tracks), row); wrong {
			seen[row.ExternalID] = true
			continue
		}
		if err := check(row.ExternalID, row.Release); err != nil {
			return MatchResult{}, err
		}
	}
	if len(passed) > 1 {
		return matchedEditions(match, passed), nil
	}
	ids, complete, err := m.sources.ReleaseGroupEditions(ctx, "musicbrainz", selected.Group.ID)
	if err != nil {
		return MatchResult{}, err
	}
	for _, id := range ids {
		if err := check(id, nil); err != nil {
			return MatchResult{}, err
		}
	}
	if len(passed) > 1 {
		return matchedEditions(match, passed), nil
	}
	if !complete || !slices.Contains(ids, selected.ID) {
		// 版本列表不完整，已有结构依据只支持发行组。
		match = groupMatch(match, []sources.Release{*selected}, "structure")
		match.Release, match.Pairs = nil, nil
	}
	return match, nil
}

func (m *ModelMatcher) checkModelPartialEditions(ctx context.Context, e catalog.CurationEvidence, item curation.PendingItem, match MatchResult) (MatchResult, error) {
	var candidates []Candidate
	if len(item.Candidates) > 0 {
		if err := json.Unmarshal(item.Candidates, &candidates); err != nil {
			return MatchResult{}, err
		}
	}
	selected := match.Candidates[0]
	for _, candidate := range candidates {
		if candidate.ExternalID == selected.ExternalID {
			continue
		}
		if candidate.ReleaseGroupID != match.GroupID && !blockedCodes(candidate.Conflicts) && needsPartialDetail(e, candidate) {
			finder, ok := m.sources.ReleaseFinder("musicbrainz")
			if !ok {
				return MatchResult{}, sources.ErrUnavailable
			}
			detail, err := found(finder.ReleaseByID(ctx, candidate.ExternalID))
			if err != nil {
				return MatchResult{}, err
			}
			if detail.ID == "" {
				continue
			}
			if !detail.Complete || !completeRelease(&detail, candidate.ExternalID) {
				return MatchResult{}, sources.ErrUnavailable
			}
			candidate.Release = &detail
		}
		match.Candidates = append(match.Candidates, candidate)
	}
	checked, rows := checkPartialAlbumMatch(e, MatchResult{Method: "model", Candidates: match.Candidates})
	if checked.GroupID != match.GroupID {
		return MatchResult{}, partialGroupConflict(rows)
	}
	match.Credits = checked.Credits
	return match, nil
}

func partialGroupConflict(rows []sources.Release) error {
	var labels []string
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Group.ID != "" && !seen[r.Group.ID] {
			seen[r.Group.ID] = true
			labels = append(labels, r.Group.ID+"《"+r.Group.Title+"》")
		}
	}
	if len(labels) > 1 {
		return operations.Conflict("track_count", "另有发行组同样吻合，无法确定。吻合的发行组为"+strings.Join(labels, "、")+"。请勿重复提交同一编号")
	}
	return operations.Conflict("track_count", "曲数不同，现有候选也不能确认唯一的部分吻合发行组")
}
