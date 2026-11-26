package program

import (
	"cmp"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type partialTrack struct {
	trackPosition
	track sources.Track
	title string
}

// 部分结构只支持组级对应。所有用于配对的曲目都要有标题、时长，且没有标识反证。
func partialAlbumRule(e catalog.CurationEvidence, r sources.Release, recordings map[string]map[string]bool) (rule string, externalTracks int) {
	if !r.Complete || !completeRelease(&r, r.ID) || r.Group.ID == "" || len(tags(e.Tracks, "mb_release")) != 0 {
		return "", 0
	}
	var c comparison
	c.compareRelease(e, "model", r)
	if len(c.conflicts) > 0 {
		return "", 0
	}
	local := e.SortedTracks()
	var remote []partialTrack
	for _, m := range r.Media {
		for _, t := range m.Tracks {
			title := t.Title
			if title == "" {
				title = t.RecordingTitle
			}
			remote = append(remote, partialTrack{trackPosition{m.Position, t.Position}, t, looseKey(title)})
		}
	}
	slices.SortFunc(remote, func(a, b partialTrack) int {
		return cmp.Or(cmp.Compare(a.disc, b.disc), cmp.Compare(a.position, b.position))
	})
	if len(local) == len(remote) || len(local) == 0 || len(remote) == 0 {
		return "", 0
	}
	for i := 1; i < len(remote); i++ {
		if remote[i].trackPosition == remote[i-1].trackPosition {
			return "", 0
		}
	}
	titles := make([]string, len(local))
	for i, t := range local {
		if i > 0 && t.Disc == local[i-1].Disc && t.Position == local[i-1].Position {
			return "", 0
		}
		if !t.TitlePlaceholder {
			titles[i] = looseKey(t.Title)
		}
	}
	matches := func(i, j int) bool {
		t, rt := local[i], remote[j].track
		if titles[i] == "" || titles[i] != remote[j].title {
			return false
		}
		_, within := compareLength(t.DurationSeconds, rt.LengthMS)
		if !within || len(linkConflicts(e, "track", t.ID, t.ExternalID, rt.RecordingID)) != 0 {
			return false
		}
		for _, id := range t.Identifiers["mb_recording"] {
			if id != rt.RecordingID {
				return false
			}
		}
		for _, id := range t.Identifiers["mb_track"] {
			if id != rt.ID {
				return false
			}
		}
		_, bad := compareISRCs(trackISRCs(t), rt.ISRCs)
		return !bad && (recordings[t.ID] == nil || recordings[t.ID][rt.RecordingID])
	}
	if len(local) > len(remote) {
		for i, rt := range remote {
			t := local[i]
			if t.Disc != rt.disc || !t.PositionInferred && t.Position != rt.position || !matches(i, i) {
				return "", 0
			}
		}
		return "partial_superset", len(remote)
	}
	if len(local) < max(4, (len(remote)+1)/2) {
		return "", 0
	}
	// 同名曲和重叠的时长容差可能有多种配对，允许前一首让出位置。
	byTitle := make(map[string][]int)
	for j, track := range remote {
		byTitle[track.title] = append(byTitle[track.title], j)
	}
	options := make([][]int, len(local))
	for i := range local {
		for _, j := range byTitle[titles[i]] {
			if matches(i, j) {
				options[i] = append(options[i], j)
			}
		}
		if len(options[i]) == 0 {
			return "", 0
		}
	}
	assigned := make([]int, len(remote))
	seen := make([]int, len(remote))
	var pair func(i, round int) bool
	pair = func(i, round int) bool {
		for _, j := range options[i] {
			if seen[j] == round {
				continue
			}
			seen[j] = round
			if assigned[j] == 0 || pair(assigned[j]-1, round) {
				assigned[j] = i + 1
				return true
			}
		}
		return false
	}
	for i := range local {
		if !pair(i, i+1) {
			return "", 0
		}
	}
	return "partial_subset", len(remote)
}

// 只使用已经取得的详情。另一组仍有未核实的候选时不能宣告组唯一。
func partialAlbumMatch(e catalog.CurationEvidence, result MatchResult) MatchResult {
	if result.Method == "tag_id" || slices.Contains(result.Conflicts, "list_truncated") ||
		slices.Contains(result.Conflicts, "ambiguous") || slices.Contains(result.Conflicts, "too_many") {
		return result
	}
	var rows []sources.Release
	var unknownGroups []string
	var rule string
	for _, candidate := range result.Candidates {
		r := candidate.Release
		if blockedCodes(candidate.Conflicts) {
			continue
		}
		if r == nil || !r.Complete {
			unknownGroups = append(unknownGroups, candidate.ReleaseGroupID)
			continue
		}
		if result.Method == "model" && len(rows) > 0 && r.Group.ID != rows[0].Group.ID {
			var c comparison
			c.compareRelease(e, "search", *r)
			c.compareTracks(e, "search", *r)
			if c.passes("search") && (len(e.Tracks) > 4 || c.titleMatch && c.artistMatch) {
				return result
			}
		}
		if matched, _ := partialAlbumRule(e, *r, result.Snapshot.RecordingIDs); matched != "" {
			rows = append(rows, *r)
			if rule == "" {
				rule = matched
			}
		}
	}
	group := sharedGroup(rows)
	if group == "" || slices.ContainsFunc(unknownGroups, func(id string) bool { return id != group }) {
		return result
	}
	result.Route, result.Conflicts = "", nil
	return groupMatch(result, rows, rule)
}
