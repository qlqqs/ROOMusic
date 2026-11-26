package program

import (
	"context"
	"errors"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// found 将无效查询和查无结果折算成空响应，其余错误交给工作单元分类。
func found[T any](value T, err error) (T, error) {
	if errors.Is(err, sources.ErrInvalidQuery) || errors.Is(err, sources.ErrNotFound) {
		var empty T
		return empty, nil
	}
	return value, err
}

type releaseList struct {
	items        []sources.Release
	codes        []string
	hit          bool
	unverified   bool
	pages        []SearchPage
	counts       map[string]curation.ReleaseCounts
	recordingIDs map[string]map[string]bool
}

func uniqueReleases(rows []sources.Release) []sources.Release {
	seen := map[string]bool{}
	out := make([]sources.Release, 0, len(rows))
	for _, r := range rows {
		if r.ID != "" && !seen[r.ID] {
			out = append(out, r)
			seen[r.ID] = true
		}
	}
	return out
}

// intersectReleases 返回各次查询都包含的发行，保持第一次查询的顺序。
func intersectReleases(lists [][]sources.Release) []sources.Release {
	if len(lists) == 0 {
		return nil
	}
	count := map[string]int{}
	for _, rows := range lists {
		for _, r := range uniqueReleases(rows) {
			count[r.ID]++
		}
	}
	out := []sources.Release{}
	for _, r := range uniqueReleases(lists[0]) {
		if count[r.ID] == len(lists) {
			out = append(out, r)
		}
	}
	return out
}

func barcodeCandidates(ctx context.Context, f sources.ReleaseFinder, barcodes []string) (releaseList, error) {
	var pages [][]sources.Release
	seen := map[string]bool{}
	for _, barcode := range barcodes {
		if barcode == "" || seen[barcode] {
			continue
		}
		seen[barcode] = true
		rows, err := found(f.ReleasesByBarcode(ctx, barcode))
		if err != nil {
			return releaseList{}, err
		}
		pages = append(pages, rows)
	}
	return releasePages(pages), nil
}

func releasePages(pages [][]sources.Release) releaseList {
	var list releaseList
	for _, rows := range pages {
		if len(rows) >= 25 {
			list.codes = []string{"list_truncated"}
		}
		list.items = append(list.items, rows...)
	}
	list.items = uniqueReleases(list.items)
	list.hit = len(list.items) > 0
	return list
}

func sampleISRCTracks(e catalog.CurationEvidence) []catalog.CurationTrack {
	var tracks []catalog.CurationTrack
	for _, t := range e.SortedTracks() {
		if len(queryISRCs(t)) > 0 {
			tracks = append(tracks, t)
		}
	}
	return sampleFour(tracks)
}

func queryISRCs(t catalog.CurationTrack) []string {
	return slices.DeleteFunc(trackISRCs(t), func(code string) bool {
		return len(metadata.AppendIdentifierValues(nil, metadata.IdentifierISRC, code)) == 0
	})
}

func sampleFour[T any](rows []T) []T {
	if len(rows) <= 4 {
		return rows
	}
	last := len(rows) - 1
	return []T{rows[0], rows[(last+1)/3], rows[(2*last+1)/3], rows[last]}
}

func isrcCandidates(ctx context.Context, f sources.RecordingFinder, e catalog.CurationEvidence) (releaseList, error) {
	tracks := sampleISRCTracks(e)
	sample := make([][]string, len(tracks))
	for i, t := range tracks {
		sample[i] = queryISRCs(t)
	}
	var pages []SearchPage
	if finder, ok := f.(curation.RecordingPageFinder); ok && len(sample) > 0 {
		var all []string
		for _, ids := range sample {
			for _, id := range ids {
				if !slices.Contains(all, id) {
					all = append(all, id)
				}
			}
		}
		page, err := found(finder.SearchRecordingPage(ctx, curation.RecordingSearch{ISRCs: all}))
		if err != nil {
			return releaseList{}, err
		}
		p := recordingSearchEvidence(page, "isrc", false)
		pages = append(pages, p)
		if len(page.Items) == 0 {
			return releaseList{pages: pages}, nil
		}
		groups := make([][]sources.Recording, len(sample))
		usable := true
		for _, rec := range page.Items {
			assigned := false
			for i, ids := range sample {
				if slices.ContainsFunc(rec.ISRCs, func(id string) bool { return slices.Contains(ids, isrcKey(id)) }) {
					groups[i] = append(groups[i], rec)
					assigned = true
				}
			}
			if !assigned || len(rec.Releases) == 0 {
				usable = false
			}
		}
		if usable {
			list := trackISRCList(tracks, groups)
			if !page.Complete || slices.ContainsFunc(page.Items, func(r sources.Recording) bool { return page.ReleaseCounts[r.ID] >= 25 }) {
				list.codes = []string{"list_truncated"}
			}
			list.pages = pages
			return list, nil
		}
	}
	var byTrack [][]sources.Recording
	cached := map[string][]sources.Recording{}
	for _, ids := range sample {
		var foundForTrack []sources.Recording
		for _, id := range ids {
			recordings, looked := cached[id]
			if !looked {
				var err error
				recordings, err = found(f.RecordingsByISRC(ctx, id))
				if err != nil {
					return releaseList{}, err
				}
				cached[id] = recordings
			}
			foundForTrack = append(foundForTrack, recordings...)
		}
		byTrack = append(byTrack, foundForTrack)
	}
	list := trackISRCList(tracks, byTrack)
	if !list.hit && len(pages) > 0 && len(pages[0].IDs) > 0 {
		list.hit = true
		list.unverified = true
	}
	list.pages = pages
	return list, nil
}

func trackISRCList(tracks []catalog.CurationTrack, byTrack [][]sources.Recording) releaseList {
	list := isrcReleaseList(byTrack)
	list.recordingIDs = map[string]map[string]bool{}
	for i, recordings := range byTrack {
		ids := map[string]bool{}
		for _, r := range recordings {
			ids[r.ID] = true
		}
		list.recordingIDs[tracks[i].ID] = ids
	}
	return list
}

func isrcReleaseList(byTrack [][]sources.Recording) releaseList {
	var list releaseList
	var all [][]sources.Release
	// 同一首的标签和 CUE 识别码取并集，各首之间再求交。
	for _, recordings := range byTrack {
		list.hit = list.hit || len(recordings) > 0
		var track []sources.Release
		for _, r := range recordings {
			if len(r.Releases) >= 25 {
				list.codes = []string{"list_truncated"}
			}
			track = append(track, r.Releases...)
		}
		all = append(all, track)
		list.items = append(list.items, track...)
	}
	list.items = uniqueReleases(list.items)
	if !list.hit || len(list.codes) > 0 {
		return list
	}
	intersection := intersectReleases(all)
	if len(intersection) == 0 {
		list.codes = []string{"isrc_spread"}
	} else {
		list.items = intersection
	}
	return list
}

func discCandidates(ctx context.Context, f sources.ReleaseFinder, e catalog.CurationEvidence) (releaseList, error) {
	var matches []sources.DiscMatch
	for _, toc := range DiscTOCs(e) {
		match, err := found(f.ReleasesByDisc(ctx, toc))
		if err != nil {
			return releaseList{}, err
		}
		matches = append(matches, match)
	}
	return discReleaseList(matches), nil
}

func discReleaseList(matches []sources.DiscMatch) releaseList {
	var list releaseList
	var all [][]sources.Release
	for _, match := range matches {
		list.hit = list.hit || len(match.Releases) > 0
		all = append(all, match.Releases)
		list.items = append(list.items, match.Releases...)
		if !match.Exact && len(match.Releases) > 0 {
			list.codes = []string{"disc_fuzzy"}
		}
	}
	list.items = uniqueReleases(list.items)
	if len(list.codes) == 0 && list.hit {
		if common := intersectReleases(all); len(common) > 0 {
			list.items = common
		} else {
			// 只有全部碟共同精确命中的发行能进入核对；不相交的结果仅供待定项参考。
			list.unverified = true
			list.codes = []string{"disc_spread"}
		}
	}
	return list
}

func fullCandidates(ctx context.Context, f sources.ReleaseFinder, e catalog.CurationEvidence, method string, list releaseList) (MatchResult, error) {
	return checkCandidates(ctx, f, e, method, list, MatchOptions{})
}

func checkCandidates(ctx context.Context, f sources.ReleaseFinder, e catalog.CurationEvidence, method string, list releaseList, options MatchOptions) (MatchResult, error) {
	result := newMatch("album", method)
	result.Candidates = releaseCandidates(method, list.items)
	result.Snapshot = MatchSnapshot{Version: 1, Method: method, Pages: list.pages, RecordingIDs: list.recordingIDs, Unverified: list.unverified}
	for i := range result.Candidates {
		c := &result.Candidates[i]
		if counts, ok := list.counts[c.ExternalID]; ok {
			c.Counts = &counts
			if counts.Total != nil {
				c.TrackCount = *counts.Total
			}
		}
	}
	blocked := markBlocked(e, result.Mode, result.Candidates)
	if len(list.items) > 100 && !slices.Contains(list.codes, "list_truncated") {
		list.codes = append(list.codes, "list_truncated")
	}
	result.Snapshot.CandidateSetComplete = !slices.Contains(list.codes, "list_truncated")
	if result.Snapshot.CandidateSetComplete && blocked > 0 && blocked == len(result.Candidates) {
		result.Status, result.Conflicts = "no_usable_candidates", candidateConflicts(result.Candidates)
		return result, nil
	}
	if len(list.codes) > 0 {
		return uncertain(result, list.codes...), nil
	}
	if list.unverified {
		return uncertain(result), nil
	}
	discCounts := localDiscCounts(e)
	var rows []sources.Release
	var partial []int
	reads := &searchDetails{finder: f, attempted: map[string]bool{}}
	for i := range result.Candidates {
		c := &result.Candidates[i]
		if blockedCodes(c.Conflicts) {
			continue
		}
		wrong := knownWrongShape(e, *c.Release)
		if c.Counts != nil {
			wrong, _ = searchShape(discCounts, len(e.Tracks), *c)
		}
		if method != "tag_id" && wrong {
			c.Conflicts = []string{"track_count"}
			if sameAlbumNames(e, *c.Release) && possiblePartialCount(len(e.Tracks), c.TrackCount) && !c.Release.Complete {
				partial = append(partial, i)
			}
			continue
		}
		rows = append(rows, *c.Release)
	}
	if len(rows) == 0 {
		return reads.partial(ctx, e, uncertain(result, candidateConflicts(result.Candidates)...), partial)
	}
	var details []int
	var named []sources.Release
	unknown := false
	for i, c := range result.Candidates {
		if len(c.Conflicts) > 0 {
			continue
		}
		details = append(details, i)
		if sameAlbumNames(e, *c.Release) {
			_, known := searchShape(discCounts, len(e.Tracks), c)
			unknown = unknown || !known
			named = append(named, *c.Release)
		}
	}
	if m, decided, err := summaryGroup(ctx, e, result, reads, details, named, unknown); decided {
		return m, err
	}
	var full []sources.Release
	for _, r := range rows {
		if !r.Complete || (method == "disc_id" && (!discHasISRCs(e, r) || missingDiscIDs(e, r))) {
			detail, err := found(f.ReleaseByID(ctx, r.ID))
			if err != nil {
				return MatchResult{}, err
			}
			if detail.ID == "" {
				continue
			}
			if detail.ID != r.ID {
				return MatchResult{}, sources.ErrUnavailable
			}
			r = detail
		}
		full = append(full, r)
	}
	if len(full) == 0 {
		return uncertain(result), nil
	}
	evidence, artists, err := checkArtistNames(ctx, e, full, options.Artists)
	if err != nil {
		return MatchResult{}, err
	}
	decided := decideAlbum(evidence, method, full, MatchSnapshot{RecordingIDs: list.recordingIDs})
	decided.Snapshot.Pages = list.pages
	decided.Snapshot.CandidateSetComplete = result.Snapshot.CandidateSetComplete
	decided.ArtistEvidence, decided.Snapshot.Artists = artists, artists
	decided.Snapshot.ArtistID = evidence.ArtistMBID
	mergeCheckedCandidates(&decided, result.Candidates)
	if decided.Status == "uncertain" {
		return reads.partial(ctx, evidence, decided, partial)
	}
	return decided, nil
}

// 碟片详情通常带每碟的真实编号；缺这一投影时按发行编号补读。
func missingDiscIDs(e catalog.CurationEvidence, r sources.Release) bool {
	return len(tags(e.Tracks, "mb_disc")) > 0 && slices.ContainsFunc(r.Media, func(m sources.Medium) bool { return len(m.DiscIDs) == 0 })
}

func discHasISRCs(e catalog.CurationEvidence, r sources.Release) bool {
	for _, t := range e.Tracks {
		if len(trackISRCs(t)) == 0 {
			continue
		}
		found := false
		for _, m := range r.Media {
			if m.Position == t.Disc {
				for _, rt := range m.Tracks {
					if rt.Position == t.Position && len(rt.ISRCs) > 0 {
						found = true
						break
					}
				}
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func legacyAlbumIDs(ctx context.Context, e catalog.CurationEvidence, f sources.ReleaseFinder) (MatchResult, error) {
	list, err := barcodeCandidates(ctx, f, e.SearchBarcodes())
	if err != nil {
		return MatchResult{}, err
	}
	if list.hit {
		return fullCandidates(ctx, f, e, "barcode", list)
	}
	if number := e.Authenticity["catalog_number"].SearchValue(e.CatalogNumber); number != "" {
		rows, err := found(f.ReleasesByCatalog(ctx, number, e.Authenticity["label"].SearchValue(e.Label)))
		if err != nil {
			return MatchResult{}, err
		}
		if len(rows) > 0 {
			return fullCandidates(ctx, f, e, "catalog_number", releasePages([][]sources.Release{rows}))
		}
	}
	return MatchResult{}, nil
}

func singleIdentifiers(ctx context.Context, e catalog.CurationEvidence, f sources.RecordingFinder) (MatchResult, error) {
	if len(e.Tracks) != 1 {
		return uncertain(newMatch("single", ""), "track_count"), nil
	}
	t := e.Tracks[0]
	recordingTags := tags(e.Tracks, "mb_recording")
	if len(recordingTags) > 1 {
		return uncertain(newMatch("single", "tag_id"), "recording_tag_mismatch"), nil
	}
	if len(recordingTags) == 1 {
		r, err := found(f.RecordingByID(ctx, recordingTags[0]))
		if err != nil {
			return MatchResult{}, err
		}
		if r.ID != "" {
			if r.ID != recordingTags[0] {
				return MatchResult{}, sources.ErrUnavailable
			}
			return DecideSingle(e, "tag_id", []sources.Recording{r}), nil
		}
	}
	ids := queryISRCs(t)
	var pages []SearchPage
	if pf, ok := f.(curation.RecordingPageFinder); ok && len(ids) > 0 {
		page, err := found(pf.SearchRecordingPage(ctx, curation.RecordingSearch{ISRCs: ids}))
		if err != nil {
			return MatchResult{}, err
		}
		pages = append(pages, recordingSearchEvidence(page, "isrc", false))
		if len(page.Items) == 0 {
			return MatchResult{Snapshot: MatchSnapshot{Pages: pages}}, nil
		}
		var rows []sources.Recording
		missing := false
		for _, r := range page.Items {
			if len(r.ISRCs) == 0 {
				missing = true
				break
			}
			if slices.ContainsFunc(r.ISRCs, func(code string) bool { return slices.Contains(ids, isrcKey(code)) }) {
				rows = append(rows, r)
			}
		}
		if !missing {
			if len(rows) == 0 {
				m := newMatch("single", "isrc")
				m.Candidates = recordingCandidates("isrc", page.Items)
				m.Snapshot.Pages = pages
				return uncertain(m, "isrc_mismatch"), nil
			}
			m := newMatch("single", "isrc")
			if !page.Complete {
				m.Candidates = recordingCandidates("isrc", rows)
				markBlocked(e, m.Mode, m.Candidates)
				m = uncertain(m, "list_truncated")
				m.Snapshot = MatchSnapshot{Version: 1, Method: "isrc"}
			} else {
				m = DecideSingle(e, "isrc", rows)
			}
			m.Snapshot.Pages = pages
			return m, nil
		}
	}
	var rows []sources.Recording
	seen := map[string]bool{}
	for _, id := range ids {
		records, err := found(f.RecordingsByISRC(ctx, id))
		if err != nil {
			return MatchResult{}, err
		}
		for _, r := range records {
			if !seen[r.ID] {
				rows = append(rows, r)
				seen[r.ID] = true
			}
		}
	}
	if len(rows) > 0 {
		m := DecideSingle(e, "isrc", rows)
		m.Snapshot.Pages = pages
		return m, nil
	}
	if len(pages) > 0 && len(pages[0].IDs) > 0 {
		m := uncertain(newMatch("single", "isrc"), "isrc_mismatch")
		m.Snapshot.Pages = pages
		return m, nil
	}
	return MatchResult{Snapshot: MatchSnapshot{Pages: pages}}, nil
}

func Match(ctx context.Context, e catalog.CurationEvidence, f sources.ReleaseFinder, rf sources.RecordingFinder) (MatchResult, error) {
	return matchWithOptions(ctx, e, f, rf, nil, MatchOptions{})
}

func MatchWithFingerprint(ctx context.Context, e catalog.CurationEvidence, f sources.ReleaseFinder, rf sources.RecordingFinder, fp *FingerprintStep) (MatchResult, error) {
	return matchWithOptions(ctx, e, f, rf, fp, MatchOptions{})
}
