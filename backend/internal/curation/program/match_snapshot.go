package program

import (
	"context"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

func candidateConflicts(rows []Candidate) []string {
	freq := map[string]int{}
	for _, r := range rows {
		for _, code := range r.Conflicts {
			freq[code]++
		}
	}
	return commonConflicts(freq)
}

// 目录中的已有对应和撤销标记可能在两次判定之间改变。
func markBlocked(e catalog.CurationEvidence, mode string, rows []Candidate) int {
	count := 0
	for i := range rows {
		c := &rows[i]
		c.Conflicts = slices.DeleteFunc(slices.Clone(c.Conflicts), blockingCode)
		var codes []string
		if mode == "single" && len(e.Tracks) == 1 {
			t := e.Tracks[0]
			codes = linkConflicts(e, "track", t.ID, t.ExternalID, c.ExternalID)
		} else {
			codes = releaseBlocks(e, c.ExternalID, c.ReleaseGroupID)
			if c.Release != nil && c.Release.Complete {
				var cmp comparison
				cmp.compareTracks(e, c.Method, *c.Release)
				for _, code := range cmp.conflicts {
					if blockingCode(code) && !slices.Contains(codes, code) {
						codes = append(codes, code)
					}
				}
			}
		}
		c.Conflicts = append(c.Conflicts, codes...)
		if len(codes) > 0 {
			count++
		}
	}
	return count
}

func mergeCheckedCandidates(m *MatchResult, original []Candidate) {
	checked := make(map[string]Candidate, len(m.Candidates))
	for _, c := range m.Candidates {
		checked[c.ExternalID] = c
	}
	for i, c := range original {
		if v, ok := checked[c.ExternalID]; ok {
			v.Counts = c.Counts
			original[i] = v
		}
	}
	m.Candidates = original
	// 详情核对只覆盖筛选后的子集，已排除的行仍可能没有链接阻止。
	if m.Status == "no_usable_candidates" && len(m.modelCandidates()) > 0 {
		m.Status, m.Route = "uncertain", "select"
		m.Conflicts = candidateConflicts(original)
	}
}

func knownWrongShape(e catalog.CurationEvidence, r sources.Release) bool {
	if len(r.Media) == 0 {
		return false
	}
	discs := e.DiscPositions()
	// 已有标识的单碟曲序仍可按现有规则与完整曲表对位。
	if len(discs) > 1 && len(discs) != len(r.Media) {
		return true
	}
	total := 0
	for _, m := range r.Media {
		n := max(m.TrackCount, len(m.Tracks))
		if n == 0 {
			return false
		}
		total += n
	}
	return len(e.Tracks) != total
}

func localDiscCounts(e catalog.CurationEvidence) []int {
	byDisc := map[int]int{}
	for _, t := range e.Tracks {
		byDisc[t.Disc]++
	}
	var out []int
	for _, d := range e.DiscPositions() {
		out = append(out, byDisc[d])
	}
	return out
}

// 返回能否排除和是否完整证实，以免把缺项当成相等或不等。
func searchShape(counts []int, total int, c Candidate) (wrong, known bool) {
	if c.Counts == nil {
		if c.Release == nil {
			return false, false
		}
		r := c.Release
		if len(r.Media) > 0 && len(r.Media) != len(counts) {
			return true, true
		}
		if !r.Complete {
			return false, false
		}
		for i, m := range r.Media {
			if len(m.Tracks) != counts[i] {
				return true, true
			}
		}
		return false, len(r.Media) > 0
	}
	v := c.Counts
	if v.Total != nil && *v.Total != total {
		return true, true
	}
	if len(v.Media) == 0 {
		return false, false
	}
	if len(v.Media) != len(counts) {
		return true, true
	}
	known = v.Total != nil
	for i, n := range v.Media {
		if n == nil {
			known = false
			continue
		}
		if *n != counts[i] {
			return true, true
		}
	}
	return false, known
}

func pageCoverage(pages []SearchPage, kind string) (complete, relaxed bool) {
	seen := false
	var inferred []string
	original := map[string]bool{}
	for _, p := range pages {
		if p.Kind != kind {
			continue
		}
		if !p.Complete {
			return false, false
		}
		seen = true
		if p.Relaxed || p.Canonical {
			inferred = append(inferred, p.IDs...)
			continue
		}
		for _, id := range p.IDs {
			original[id] = true
		}
	}
	return seen, slices.ContainsFunc(inferred, func(id string) bool { return !original[id] })
}

func decideSearchSingle(e catalog.CurationEvidence, m MatchResult) MatchResult {
	ok, inferred := pageCoverage(m.Snapshot.Pages, "recording")
	if !ok {
		markBlocked(e, m.Mode, m.Candidates)
		return uncertain(m, "list_truncated")
	}
	decided := decideRecordingText(e, m)
	if inferred && decided.Level == "artist" {
		m.Candidates = decided.Candidates
		return uncertain(m, "ambiguous")
	}
	return decided
}

func needsFullGroupCheck(e catalog.CurationEvidence) bool {
	seen := map[trackPosition]bool{}
	for _, t := range e.Tracks {
		key := trackPosition{t.Disc, t.Position}
		if seen[key] {
			return true
		}
		seen[key] = true
		if t.PositionInferred || len(trackISRCs(t)) > 0 || t.ExternalID != "" {
			return true
		}
		for _, key := range []string{"mb_track", "mb_recording", "mb_disc"} {
			if len(t.Identifiers[key]) > 0 {
				return true
			}
		}
	}
	return false
}

func decideSearchAlbum(ctx context.Context, e catalog.CurationEvidence, result MatchResult, f sources.ReleaseFinder, offline bool) (MatchResult, error) {
	ok, relaxed := pageCoverage(result.Snapshot.Pages, "release")
	if !ok {
		markBlocked(e, result.Mode, result.Candidates)
		return uncertain(result, "list_truncated"), nil
	}
	discCounts := localDiscCounts(e)
	markBlocked(e, result.Mode, result.Candidates)
	var groupRows []sources.Release
	var details []int
	blocked := 0
	unknownGroup := false
	for i := range result.Candidates {
		c := &result.Candidates[i]
		if c.Release == nil {
			return uncertain(result), nil
		}
		var cmp comparison
		for _, code := range c.Conflicts {
			if blockingCode(code) {
				cmp.conflict(code)
			}
		}
		cmp.compareRelease(e, "search", *c.Release)
		// 摘要的空字段不能形成反证，先读详情才能核实。
		unknown := false
		if !c.Release.Complete {
			cmp.conflicts = slices.DeleteFunc(cmp.conflicts, func(code string) bool {
				missing := code == "disc_tag_mismatch" ||
					code == "media_format_mismatch" && (len(c.Release.Media) == 0 || slices.ContainsFunc(c.Release.Media, func(m sources.Medium) bool { return m.Format == "" })) ||
					code == "group_tag_mismatch" && c.Release.Group.ID == "" ||
					code == "artist_tag_mismatch" && (len(c.Release.Artists) == 0 || slices.ContainsFunc(c.Release.Artists, func(a sources.Credit) bool { return a.ArtistID == "" }))
				unknown = unknown || missing
				return missing
			})
			unknown = unknown || len(e.Barcodes) > 0 && c.Release.Barcode == "" || e.CatalogNumber != "" && !slices.ContainsFunc(c.Release.Labels, func(l sources.Label) bool { return strings.TrimSpace(l.CatalogNumber) != "" })
		}
		wrong, known := searchShape(discCounts, len(e.Tracks), *c)
		if wrong {
			cmp.conflict("track_count")
		}
		c.Conflicts = cmp.conflicts
		if len(c.Conflicts) > 0 {
			if blockedCodes(c.Conflicts) {
				blocked++
			}
			continue
		}
		details = append(details, i)
		if cmp.titleMatch && cmp.artistMatch {
			if !known || unknown {
				unknownGroup = true
			}
			groupRows = append(groupRows, *c.Release)
		}
	}
	if blocked > 0 && blocked == len(result.Candidates) {
		result.Status, result.Route, result.Conflicts = "no_usable_candidates", "", candidateConflicts(result.Candidates)
		return result, nil
	}
	groupCertain := !relaxed && !unknownGroup && e.Authenticity["title"] == metadata.Real && e.SelectedArtistAuth() == metadata.Real && !e.Compilation && !e.Classical && len(e.Tracks) > 4 && sharedGroup(groupRows) != "" && !needsFullGroupCheck(e)
	groupDurationsOK := func(rows []sources.Release) bool {
		if !groupCertain {
			return false
		}
		if !slices.ContainsFunc(e.Tracks, func(t catalog.CurationTrack) bool { return t.DurationSeconds > 0 }) {
			return true
		}
		for _, summary := range groupRows {
			i := slices.IndexFunc(rows, func(r sources.Release) bool { return r.ID == summary.ID })
			if i < 0 || !rows[i].Complete {
				return false
			}
			var c comparison
			c.compareRelease(e, "search", rows[i])
			c.compareTracks(e, "search", rows[i])
			if !c.countEqual || !c.titleMatch || !c.artistMatch || rows[i].Group.ID != summary.Group.ID || len(c.conflicts) > 0 {
				return false
			}
		}
		return true
	}
	groupVerified := groupDurationsOK(groupRows)
	fillTitles := slices.ContainsFunc(e.Tracks, func(t catalog.CurationTrack) bool { return t.TitlePlaceholder || t.TitleInferred })
	if groupVerified && !fillTitles {
		return groupMatch(result, groupRows, "unique_group"), nil
	}
	if len(details) == 0 {
		return partialAlbumMatch(e, uncertain(result, candidateConflicts(result.Candidates)...)), nil
	}
	if len(details) > 3 {
		if groupVerified {
			return groupMatch(result, groupRows, "unique_group"), nil
		}
		return uncertain(result, "too_many"), nil
	}
	var rows []sources.Release
	for _, i := range details {
		c := &result.Candidates[i]
		r := *c.Release
		if !r.Complete {
			if offline {
				if groupVerified {
					return groupMatch(result, groupRows, "unique_group"), nil
				}
				return uncertain(result, "insufficient_evidence"), nil
			}
			detail, err := found(f.ReleaseByID(ctx, c.ExternalID))
			if err != nil {
				return MatchResult{}, err
			}
			if detail.ID == "" {
				continue
			}
			if detail.ID != c.ExternalID {
				return MatchResult{}, sources.ErrUnavailable
			}
			r = detail
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		if groupVerified {
			return groupMatch(result, groupRows, "unique_group"), nil
		}
		return uncertain(result), nil
	}
	groupVerified = groupDurationsOK(rows)
	decided := decideAlbum(e, "search", rows, MatchSnapshot{})
	mergeCheckedCandidates(&decided, result.Candidates)
	decided.Snapshot = result.Snapshot
	decided.ArtistEvidence = result.ArtistEvidence
	if groupVerified && (!fillTitles || decided.Status == "uncertain") && !slices.Contains(decided.Conflicts, "ambiguous") && !slices.ContainsFunc(decided.Candidates, func(c Candidate) bool {
		return blockedCodes(c.Conflicts) || slices.Contains(c.Conflicts, "group_tag_mismatch")
	}) {
		result.Candidates = decided.Candidates
		return groupMatch(result, groupRows, "unique_group"), nil
	}
	if decided.Status == "uncertain" {
		return partialAlbumMatch(e, decided), nil
	}
	return decided, nil
}

func replayMatch(e catalog.CurationEvidence, candidates []Candidate, conflicts []string, snapshot MatchSnapshot) MatchResult {
	e.Compilation = curation.CompilationEvidence(e)
	if len(snapshot.Blocked) > 0 {
		candidates = append(slices.Clone(candidates), snapshot.Blocked...)
		snapshot.Blocked = nil
	}
	if len(candidates) == 0 {
		return MatchResult{}
	}
	mode := "album"
	if e.CandidateKind == "loose_unknown" {
		mode = "single"
		if len(e.Tracks) != 1 {
			return MatchResult{}
		}
	}
	method := snapshot.Method
	if method == "" {
		method = candidates[0].Method
	}
	base := newMatch(mode, method)
	base.Candidates, base.Snapshot, base.Offline, base.ArtistEvidence = slices.Clone(candidates), snapshot, true, snapshot.Artists
	if e.ArtistMBID == "" {
		e.ArtistMBID = snapshot.ArtistID
	}
	blocked := markBlocked(e, mode, base.Candidates)
	base.Conflicts = slices.DeleteFunc(slices.Clone(conflicts), blockingCode)
	for _, code := range candidateConflicts(base.Candidates) {
		if !slices.Contains(base.Conflicts, code) {
			base.Conflicts = append(base.Conflicts, code)
		}
	}
	pending := uncertain(base)
	for _, c := range base.Candidates {
		if c.Source != "musicbrainz" || c.ExternalID == "" {
			return pending
		}
	}
	// 旧的十条摘要无法证明文字页完整。唯一标签则直接限定了目标。
	complete := snapshot.CandidateSetComplete
	if snapshot.Version == 0 {
		complete = method != "search" && len(candidates) < 10
	}
	if method == "search" {
		complete = completeSearchSnapshot(mode, snapshot)
	}
	if blocked == len(candidates) && (method == "tag_id" || complete && !slices.Contains(conflicts, "list_truncated")) {
		base.Status, base.Conflicts = "no_usable_candidates", candidateConflicts(base.Candidates)
		return base
	}
	if snapshot.Version != 1 || snapshot.Unverified {
		return pending
	}
	var m MatchResult
	base.Conflicts = nil
	base.Candidates = slices.Clone(pending.Candidates)
	if method == "search" {
		e = textSearchEvidence(curation.LocalSearchEvidence(e), mode, snapshot)
		if mode == "single" {
			for _, c := range candidates {
				if c.Recording == nil {
					return pending
				}
			}
			m = decideSearchSingle(e, base)
		} else {
			m, _ = decideSearchAlbum(context.Background(), e, base, nil, true)
		}
	} else {
		// 只有标识候选来源完整、没有列表级反证时才离线重算。
		for _, code := range []string{"list_truncated", "isrc_spread", "disc_fuzzy", "acoustid_spread", "acoustid_sparse", "too_many"} {
			if slices.Contains(conflicts, code) {
				return pending
			}
		}
		if mode == "single" {
			var rows []sources.Recording
			for _, c := range candidates {
				if c.Recording == nil {
					return pending
				}
				rows = append(rows, *c.Recording)
			}
			m = DecideSingle(e, method, rows)
		} else {
			var rows []sources.Release
			for _, c := range candidates {
				if c.Release == nil || !c.Release.Complete {
					return pending
				}
				rows = append(rows, *c.Release)
			}
			m = decideAlbum(e, method, rows, snapshot)
			if m.Status == "uncertain" {
				m = partialAlbumMatch(e, m)
			}
		}
	}
	if m.Status == "uncertain" && slices.Contains(m.Conflicts, "insufficient_evidence") {
		return uncertain(pending, "insufficient_evidence")
	}
	if m.Status == "uncertain" {
		for _, code := range pending.Conflicts {
			if !slices.Contains(m.Conflicts, code) {
				m.Conflicts = append(m.Conflicts, code)
			}
		}
	}
	m.Offline, m.ArtistEvidence, m.Snapshot = true, snapshot.Artists, snapshot
	return m
}
func completeSearchSnapshot(mode string, s MatchSnapshot) bool {
	kind := "release"
	if mode == "single" {
		kind = "recording"
	}
	ok, _ := pageCoverage(s.Pages, kind)
	return ok
}
