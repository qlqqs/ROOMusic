package program

import (
	"context"
	"errors"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
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
	items      []sources.Release
	codes      []string
	hit        bool
	unverified bool
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

func sampleISRCs(e catalog.CurationEvidence) [][]string {
	var rows [][]string
	for _, t := range sortedTracks(e) {
		if ids := trackISRCs(t); len(ids) > 0 {
			rows = append(rows, ids)
		}
	}
	if len(rows) <= 4 {
		return rows
	}
	last := len(rows) - 1
	return [][]string{rows[0], rows[(last+1)/3], rows[(2*last+1)/3], rows[last]}
}

func isrcCandidates(ctx context.Context, f sources.RecordingFinder, e catalog.CurationEvidence) (releaseList, error) {
	var byTrack [][]sources.Recording
	for _, ids := range sampleISRCs(e) {
		var foundForTrack []sources.Recording
		for _, id := range ids {
			recordings, err := found(f.RecordingsByISRC(ctx, id))
			if err != nil {
				return releaseList{}, err
			}
			foundForTrack = append(foundForTrack, recordings...)
		}
		byTrack = append(byTrack, foundForTrack)
	}
	return isrcReleaseList(byTrack), nil
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
		}
	}
	return list
}

func fullCandidates(ctx context.Context, f sources.ReleaseFinder, e catalog.CurationEvidence, method string, list releaseList) (MatchResult, error) {
	result := newMatch("album", method)
	result.Candidates = releaseCandidates(method, list.items)
	switch {
	case len(list.codes) > 0:
		return uncertain(result, list.codes...), nil
	case list.unverified:
		return uncertain(result), nil
	case len(list.items) > 3:
		return uncertain(result, "too_many"), nil
	}
	full := make([]sources.Release, 0, len(list.items))
	for _, row := range list.items {
		r, err := found(f.ReleaseByID(ctx, row.ID))
		if err != nil {
			return MatchResult{}, err
		}
		if r.ID != "" {
			full = append(full, r)
		}
	}
	if len(full) == 0 {
		return uncertain(result), nil
	}
	return DecideAlbum(e, method, uniqueReleases(full)), nil
}

func matchAlbum(ctx context.Context, e catalog.CurationEvidence, f sources.ReleaseFinder, rf sources.RecordingFinder) (MatchResult, error) {
	ids := tags(e.Tracks, "mb_release")
	if len(ids) > 1 {
		return DecideAlbum(e, "tag_id", nil), nil
	}
	if len(ids) == 1 {
		r, err := found(f.ReleaseByID(ctx, ids[0]))
		if err != nil {
			return MatchResult{}, err
		}
		if r.ID != "" {
			return DecideAlbum(e, "tag_id", []sources.Release{r}), nil
		}
	}
	list, err := barcodeCandidates(ctx, f, e.Barcodes)
	if err != nil {
		return MatchResult{}, err
	}
	if list.hit {
		return fullCandidates(ctx, f, e, "barcode", list)
	}
	if e.CatalogNumber != "" {
		items, err := found(f.ReleasesByCatalog(ctx, e.CatalogNumber, e.Label))
		if err != nil {
			return MatchResult{}, err
		}
		if len(items) > 0 {
			list = releasePages([][]sources.Release{items})
			return fullCandidates(ctx, f, e, "catalog_number", list)
		}
	}
	list, err = isrcCandidates(ctx, rf, e)
	if err != nil {
		return MatchResult{}, err
	}
	if list.hit {
		return fullCandidates(ctx, f, e, "isrc", list)
	}
	list, err = discCandidates(ctx, f, e)
	if err != nil {
		return MatchResult{}, err
	}
	if list.hit {
		return fullCandidates(ctx, f, e, "disc_id", list)
	}
	if !e.TitlePlaceholder && !e.ArtistPlaceholder && e.Title != "" && albumArtist(e) != "" {
		items, err := found(f.SearchReleases(ctx, sources.ReleaseQuery{Title: e.Title, Artist: albumArtist(e), TrackCount: len(e.Tracks)}))
		if err != nil {
			return MatchResult{}, err
		}
		return DecideAlbum(e, "search", uniqueReleases(items)), nil
	}
	return newMatch("album", ""), nil
}

func matchSingle(ctx context.Context, e catalog.CurationEvidence, f sources.RecordingFinder) (MatchResult, error) {
	if len(e.Tracks) != 1 {
		return uncertain(newMatch("single", ""), "track_count"), nil
	}
	t := e.Tracks[0]
	if ids := tags(e.Tracks, "mb_recording"); len(ids) == 1 {
		r, err := found(f.RecordingByID(ctx, ids[0]))
		if err != nil {
			return MatchResult{}, err
		}
		if r.ID != "" {
			return DecideSingle(e, "tag_id", []sources.Recording{r}), nil
		}
	}
	var rows []sources.Recording
	seen := map[string]bool{}
	for _, id := range trackISRCs(t) {
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
		return DecideSingle(e, "isrc", rows), nil
	}
	if !t.TitlePlaceholder && !t.ArtistPlaceholder && t.Title != "" && t.Artist != "" {
		query := sources.RecordingQuery{Title: t.Title, Artist: t.Artist}
		if t.DurationSeconds > 0 {
			ms := int64(t.DurationSeconds) * 1000
			query.LengthMS = &ms
		}
		records, err := found(f.SearchRecordings(ctx, query))
		if err != nil {
			return MatchResult{}, err
		}
		return DecideSingle(e, "search", records), nil
	}
	return newMatch("single", ""), nil
}

// Match 按可靠程度查询，遇到首个命中就返回该步的判定。工作者传入闸门提供的能力。
func Match(ctx context.Context, e catalog.CurationEvidence, f sources.ReleaseFinder, rf sources.RecordingFinder) (MatchResult, error) {
	if err := ctx.Err(); err != nil {
		return MatchResult{}, err
	}
	mode := "album"
	titlePlaceholder, artistPlaceholder := e.TitlePlaceholder, e.ArtistPlaceholder
	if e.CandidateKind == "loose_unknown" {
		mode = "single"
		if len(e.Tracks) == 1 {
			titlePlaceholder, artistPlaceholder = e.Tracks[0].TitlePlaceholder, e.Tracks[0].ArtistPlaceholder
		}
	}
	if titlePlaceholder && artistPlaceholder && len(e.Barcodes) == 0 && e.CatalogNumber == "" && len(DiscTOCs(e)) == 0 {
		hasID := slices.ContainsFunc(e.Tracks, func(t catalog.CurationTrack) bool {
			if len(trackISRCs(t)) > 0 {
				return true
			}
			for _, ids := range t.Identifiers {
				if len(ids) > 0 {
					return true
				}
			}
			return false
		})
		if !hasID {
			r := newMatch(mode, "")
			r.Conflicts = append(r.Conflicts, "insufficient_evidence")
			return r, nil
		}
	}
	if mode == "single" {
		return matchSingle(ctx, e, rf)
	}
	return matchAlbum(ctx, e, f, rf)
}
