package program

import (
	"context"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
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
		known := len(r.Media) > 0
		for i, m := range r.Media {
			n := max(m.TrackCount, len(m.Tracks))
			if n == 0 && !r.Complete {
				known = false
				continue
			}
			if n != counts[i] {
				return true, true
			}
		}
		return false, known
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
		if len(trackISRCs(t)) > 0 || t.ExternalID != "" {
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

// 同一轮核对共用已读取的版本详情。
type searchDetails struct {
	finder    sources.ReleaseFinder
	attempted map[string]bool
}

func (d *searchDetails) read(ctx context.Context, c *Candidate) error {
	if c.Release.Complete || d == nil || d.attempted[c.ExternalID] {
		return nil
	}
	d.attempted[c.ExternalID] = true
	r, err := queryResult(d.finder.ReleaseByID(ctx, c.ExternalID))
	if err != nil || r.ID == "" {
		return err
	}
	if r.ID != c.ExternalID {
		return sources.ErrInvalidResponse
	}
	c.Release = &r
	return nil
}

func (d *searchDetails) partial(ctx context.Context, e catalog.CurationEvidence, m MatchResult, indices []int) (MatchResult, error) {
	for _, i := range indices {
		if err := d.read(ctx, &m.Candidates[i]); err != nil {
			return MatchResult{}, err
		}
	}
	return partialAlbumMatch(e, m), nil
}

func possiblePartialCount(local, remote int) bool {
	return remote > 0 && (local > remote || local >= 4 && local < remote && local*2 >= remote)
}

func summaryGroup(ctx context.Context, e catalog.CurationEvidence, result MatchResult, reads *searchDetails, details []int, groupRows []sources.Release, unknownGroup bool) (MatchResult, bool, error) {
	if !e.Classical && !e.Compilation && len(e.Tracks) > 4 && len(groupRows) > 1 && !unknownGroup && sharedGroup(groupRows) == "" &&
		!slices.ContainsFunc(groupRows, func(r sources.Release) bool { return r.Group.ID == "" }) {
		return uncertain(result, "different_release_groups"), true, nil
	}
	if len(details) > 1 && len(groupRows) == len(details) && !unknownGroup && !e.Compilation && !e.Classical && len(e.Tracks) > 4 &&
		sharedGroup(groupRows) != "" && !needsFullGroupCheck(e) {
		consistent := true
		for _, r := range groupRows {
			var releaseCheck comparison
			releaseCheck.compareRelease(e, result.Method, r)
			if len(releaseCheck.conflicts) > 0 {
				consistent = false
				break
			}
			if !r.Complete {
				continue
			}
			var c comparison
			c.compareTracks(e, "search", r)
			if len(c.conflicts) > 0 {
				consistent = false
				break
			}
		}
		if consistent {
			m := groupMatch(result, groupRows, "unique_group")
			if !slices.ContainsFunc(e.Tracks, func(t catalog.CurationTrack) bool { return t.TitleInferred && !t.TitleCorrected && !t.TitleAutomatic }) {
				return m, true, nil
			}
			i := details[0]
			for _, j := range details[1:] {
				if earlierEdition(*result.Candidates[j].Release, *result.Candidates[i].Release) {
					i = j
				}
			}
			candidate := &result.Candidates[i]
			if err := reads.read(ctx, candidate); err != nil {
				return MatchResult{}, true, err
			}
			if !candidate.Release.Complete {
				return m, true, nil
			}
			var checked comparison
			checked.compareTracks(e, "search", *candidate.Release)
			if len(checked.conflicts) > 0 {
				return uncertain(result, checked.conflicts...), true, nil
			}
			m.TrackTitles, m.RepresentativeID = checked.pairedTitles, candidate.ExternalID
			return m, true, nil
		}
	}
	return MatchResult{}, false, nil
}

func decideSearchAlbum(ctx context.Context, e catalog.CurationEvidence, result MatchResult, reads *searchDetails) (MatchResult, error) {
	ok, _ := pageCoverage(result.Snapshot.Pages, "release")
	if !ok {
		markBlocked(e, result.Mode, result.Candidates)
		return uncertain(result, "list_truncated"), nil
	}
	discCounts := localDiscCounts(e)
	markBlocked(e, result.Mode, result.Candidates)
	var groupRows []sources.Release
	var details, partialExact []int
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
			if len(cmp.conflicts) == 0 && possiblePartialCount(len(e.Tracks), c.TrackCount) && !c.Release.Complete {
				if sameAlbumNames(e, *c.Release) {
					partialExact = append(partialExact, i)
				}

			}
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
		if sameAlbumNames(e, *c.Release) {
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
	if m, decided, err := summaryGroup(ctx, e, result, reads, details, groupRows, unknownGroup); decided {
		return m, err
	}

	if len(details) == 0 {
		return reads.partial(ctx, e, uncertain(result, candidateConflicts(result.Candidates)...), partialExact)
	}
	var rows []sources.Release
	for _, i := range details {
		c := &result.Candidates[i]
		if err := reads.read(ctx, c); err != nil {
			return MatchResult{}, err
		}
		if !c.Release.Complete {
			if reads == nil || !reads.attempted[c.ExternalID] {
				return uncertain(result, "insufficient_evidence"), nil
			}
			continue
		}
		rows = append(rows, *c.Release)
	}
	if len(rows) == 0 {
		return reads.partial(ctx, e, uncertain(result), partialExact)
	}
	decided := decideAlbum(e, "search", rows, MatchSnapshot{})
	mergeCheckedCandidates(&decided, result.Candidates)
	decided.Snapshot = result.Snapshot
	decided.ArtistEvidence = result.ArtistEvidence
	if decided.Status == "uncertain" {
		return reads.partial(ctx, e, decided, partialExact)
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
			m, _ = decideSearchAlbum(context.Background(), e, base, nil)
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
