package program

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type SearchNames struct{ Title, Artist, ArtistID string }
type MatchOptions struct {
	Artists     sources.ArtistFinder
	Names       SearchNames
	ProgramOnly bool
}
type SearchPage struct {
	curation.PageInfo
	Kind          string         `json:"kind"`
	ReleaseCounts map[string]int `json:"release_counts,omitempty"`
	IDs           []string       `json:"ids,omitempty"`
	Canonical     bool           `json:"canonical,omitempty"`
	Relaxed       bool           `json:"relaxed,omitempty"`
}
type MatchSnapshot struct {
	NameOnlyArtist       bool                       `json:"name_only_artist,omitempty"`
	FormatRelaxed        bool                       `json:"format_relaxed,omitempty"`
	TagArtist            string                     `json:"tag_artist,omitempty"`
	TagReleaseID         string                     `json:"tag_release_id,omitempty"`
	Unverified           bool                       `json:"unverified,omitempty"`
	CandidateSetComplete bool                       `json:"candidate_set_complete,omitempty"`
	Blocked              []Candidate                `json:"blocked,omitempty"`
	Version              int                        `json:"version"`
	Method               string                     `json:"method"`
	Pages                []SearchPage               `json:"pages,omitempty"`
	ArtistID             string                     `json:"artist_id,omitempty"`
	Artists              map[string]sources.Artist  `json:"artists,omitempty"`
	RecordingIDs         map[string]map[string]bool `json:"recording_ids,omitempty"`
}

func matchWithOptions(ctx context.Context, e catalog.CurationEvidence, f sources.ReleaseFinder, rf sources.RecordingFinder, fp *FingerprintStep, opts MatchOptions) (MatchResult, error) {
	if err := ctx.Err(); err != nil {
		return MatchResult{}, err
	}
	e.Compilation = curation.CompilationEvidence(e)
	mode := "album"
	if e.CandidateKind == "loose_unknown" {
		mode = "single"
		if len(e.Tracks) != 1 {
			return uncertain(newMatch(mode, ""), "track_count"), nil
		}
	}
	searchInput := curation.LocalSearchEvidence(e)
	base := localNames(searchInput, mode)
	if opts.Names.ArtistID == "" {
		opts.Names.ArtistID = base.ArtistID
	}
	names := base
	if base.Title != "" && opts.Names.Title != "" {
		names.Title = opts.Names.Title
	}
	if base.Artist != "" && opts.Names.Artist != "" {
		names.Artist = opts.Names.Artist
	}
	if opts.Names.ArtistID != "" {
		names.ArtistID = opts.Names.ArtistID
	}
	if e.Compilation && mode == "album" {
		names.ArtistID = variousArtistsID
	}
	e.ArtistMBID = base.ArtistID

	var text MatchResult
	var prior []SearchPage
	insufficient := insufficientEvidence(searchInput)
	pf, paged := f.(curation.ReleasePageFinder)
	if !insufficient {
		if mode == "single" {
			m, err := singleIdentifiers(ctx, e, rf)
			if m.Status != "" || err != nil {
				return m, err
			}
			prior = m.Snapshot.Pages
		} else {
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
					if r.ID != ids[0] {
						return MatchResult{}, sources.ErrUnavailable
					}
					ev, artists, err := checkArtistNames(ctx, e, []sources.Release{r}, opts.Artists)
					if err != nil {
						return MatchResult{}, err
					}
					m := DecideAlbum(ev, "tag_id", []sources.Release{r})
					if slices.Contains(m.Candidates[0].Conflicts, "tag_text_mismatch") {
						if artists == nil {
							artists = map[string]sources.Artist{}
						}
						agree, err := tagArtistNamesAgree(ctx, r, albumArtist(e), opts.Names.Artist, opts.Artists, artists)
						if err != nil {
							return MatchResult{}, err
						}
						if agree {
							m = decideAlbum(ev, "tag_id", []sources.Release{r}, MatchSnapshot{TagArtist: albumArtist(e), TagReleaseID: r.ID})
						}
					}
					m.ArtistEvidence, m.Snapshot.Artists, m.Snapshot.ArtistID = artists, artists, ev.ArtistMBID
					return m, nil
				}
			}
			if paged {
				q := releaseQuery(names, len(e.Tracks))
				q.Barcodes = e.SearchBarcodes()
				q.CatalogNumber = e.Authenticity["catalog_number"].SearchValue(e.CatalogNumber)
				q.Label = e.Authenticity["label"].SearchValue(e.Label)
				if q.Title != "" || len(q.Barcodes) > 0 || q.CatalogNumber != "" {
					page, err := found(pf.SearchReleasePage(ctx, q))
					if err != nil {
						return MatchResult{}, err
					}
					text = albumPageResult(page, false)
					text.Snapshot.Pages[0].Canonical = canonicalQuery(base, names)
					strong, method := identifierHits(q, page.Items)
					if len(strong) > 0 {
						list := releaseList{items: strong, pages: text.Snapshot.Pages, counts: page.Counts}
						if !page.Complete {
							list.codes = []string{"list_truncated"}
						}
						return checkCandidates(ctx, f, e, method, list, opts)
					}
					if q.Title == "" {
						prior = text.Snapshot.Pages
						prior[0].Kind = "identifier"
						text = newMatch(mode, "")
					}
				}
			} else {
				m, err := legacyAlbumIDs(ctx, e, f)
				if err != nil || m.Status != "" {
					return m, err
				}
			}
			isrcs, err := isrcCandidates(ctx, rf, e)
			if err != nil {
				return MatchResult{}, err
			}
			prior = append(prior, isrcs.pages...)
			if isrcs.hit {
				isrcs.pages = append(slices.Clone(text.Snapshot.Pages), prior...)
				return checkCandidates(ctx, f, e, "isrc", isrcs, opts)
			}
			discs, err := discCandidates(ctx, f, e)
			if err != nil {
				return MatchResult{}, err
			}
			if discs.hit {
				discs.pages = append(slices.Clone(text.Snapshot.Pages), prior...)
				return checkCandidates(ctx, f, e, "disc_id", discs, opts)
			}
		}
	}
	fingerprint := "unavailable"
	if fp != nil && !opts.ProgramOnly {
		m, state, err := fp.match(ctx, mode, e, f, rf)
		fingerprint = state
		if m.Status != "" || err != nil {
			m.Snapshot.Pages = append(append(slices.Clone(text.Snapshot.Pages), prior...), m.Snapshot.Pages...)
			m.Fingerprint = state
			return m, err
		}
	}
	if insufficient {
		m := newMatch(mode, "")
		m.Fingerprint = fingerprint
		m.Conflicts = []string{"insufficient_evidence"}
		return m, nil
	}
	m, err := textSearch(ctx, searchInput, mode, base, names, f, rf, opts, text, prior)
	m.Fingerprint = fingerprint
	return m, err
}

func localNames(e catalog.CurationEvidence, mode string) SearchNames {
	n := SearchNames{ArtistID: e.ArtistMBID}
	if mode == "single" {
		t := e.Tracks[0]
		if !t.TitlePlaceholder {
			n.Title = t.Title
		}
		if !t.ArtistPlaceholder {
			n.Artist = t.Artist
		}
	} else {
		if !e.TitlePlaceholder {
			n.Title = e.Title
		}
		if !e.ArtistPlaceholder {
			n.Artist = albumArtist(e)
		}
		if e.Compilation {
			n.ArtistID = variousArtistsID
		}
	}
	return n
}
func canonicalQuery(original, names SearchNames) bool {
	return original.Title != names.Title || original.ArtistID != names.ArtistID || names.ArtistID == "" && original.Artist != names.Artist
}

func releaseQuery(n SearchNames, count int) curation.ReleaseSearch {
	if n.Title == "" || n.Artist == "" && n.ArtistID == "" {
		return curation.ReleaseSearch{}
	}
	return curation.ReleaseSearch{Title: n.Title, Artist: n.Artist, ArtistID: n.ArtistID, TrackCount: count}
}
func identifierHits(q curation.ReleaseSearch, rows []sources.Release) ([]sources.Release, string) {
	var barcode, catalog []sources.Release
	for _, r := range rows {
		if k := barcodeKey(r.Barcode); k != "" && slices.ContainsFunc(q.Barcodes, func(s string) bool { return k == barcodeKey(s) }) {
			barcode = append(barcode, r)
		}
		if q.CatalogNumber != "" && slices.ContainsFunc(r.Labels, func(l sources.Label) bool {
			return textnorm.CatalogNumber(l.CatalogNumber) == textnorm.CatalogNumber(q.CatalogNumber) && (q.Label == "" || sameText(q.Label, l.Name))
		}) {
			catalog = append(catalog, r)
		}
	}
	if len(barcode) > 0 {
		return barcode, "barcode"
	}
	return catalog, "catalog_number"
}
func albumPageResult(page curation.ReleasePage, relaxed bool) MatchResult {
	m := newMatch("album", "search")
	m.Candidates = releaseCandidates("search", uniqueReleases(page.Items))
	p := SearchPage{PageInfo: page.PageInfo, Kind: "release", Relaxed: relaxed}
	for _, r := range page.Items {
		p.IDs = append(p.IDs, r.ID)
	}
	for i := range m.Candidates {
		c := &m.Candidates[i]
		if n, ok := page.Counts[c.ExternalID]; ok {
			c.Counts = &n
			if n.Total != nil {
				c.TrackCount = *n.Total
			}
		}
	}
	m.Snapshot = MatchSnapshot{Version: 1, Method: "search", Pages: []SearchPage{p}}
	return m
}
func recordingPageResult(page curation.RecordingPage, relaxed bool) MatchResult {
	m := newMatch("single", "search")
	m.Candidates = recordingCandidates("search", page.Items)
	p := recordingSearchEvidence(page, "recording", relaxed)
	m.Snapshot = MatchSnapshot{Version: 1, Method: "search", Pages: []SearchPage{p}}
	return m
}

func recordingSearchEvidence(page curation.RecordingPage, kind string, relaxed bool) SearchPage {
	p := SearchPage{PageInfo: page.PageInfo, Kind: kind, Relaxed: relaxed, ReleaseCounts: page.ReleaseCounts}
	for _, r := range page.Items {
		p.IDs = append(p.IDs, r.ID)
	}
	return p
}

func queryText(ctx context.Context, mode string, e catalog.CurationEvidence, n SearchNames, count int, relaxed bool, f sources.ReleaseFinder, rf sources.RecordingFinder) (MatchResult, error) {
	if mode == "album" {
		q := releaseQuery(n, count)
		if q.Title == "" {
			return newMatch(mode, ""), nil
		}
		if pf, ok := f.(curation.ReleasePageFinder); ok {
			p, err := found(pf.SearchReleasePage(ctx, q))
			return albumPageResult(p, relaxed), err
		}
		rows, err := found(f.SearchReleases(ctx, sources.ReleaseQuery{Title: q.Title, Artist: q.Artist, TrackCount: q.TrackCount}))
		return albumPageResult(curation.ReleasePage{Items: rows}, relaxed), err
	}
	if n.Title == "" || n.Artist == "" && n.ArtistID == "" {
		return newMatch(mode, ""), nil
	}
	q := curation.RecordingSearch{Title: n.Title, Artist: n.Artist, ArtistID: n.ArtistID}
	if e.Tracks[0].DurationSeconds > 0 {
		ms := int64(e.Tracks[0].DurationSeconds) * 1000
		q.LengthMS = &ms
	}
	if pf, ok := rf.(curation.RecordingPageFinder); ok {
		p, err := found(pf.SearchRecordingPage(ctx, q))
		return recordingPageResult(p, relaxed), err
	}
	rows, err := found(rf.SearchRecordings(ctx, sources.RecordingQuery{Title: q.Title, Artist: q.Artist, LengthMS: q.LengthMS}))
	return recordingPageResult(curation.RecordingPage{Items: rows}, relaxed), err
}

func appendSearch(result, next MatchResult) MatchResult {
	if result.Method == "" {
		next.Snapshot.Pages = append(slices.Clone(result.Snapshot.Pages), next.Snapshot.Pages...)
		return next
	}
	result.Snapshot.Pages = append(result.Snapshot.Pages, next.Snapshot.Pages...)
	seen := make(map[string]bool, len(result.Candidates))
	for _, r := range result.Candidates {
		seen[r.ExternalID] = true
	}
	for _, r := range next.Candidates {
		if !seen[r.ExternalID] {
			result.Candidates = append(result.Candidates, r)
			seen[r.ExternalID] = true
		}
	}
	slices.SortFunc(result.Candidates, func(a, b Candidate) int { return strings.Compare(a.ExternalID, b.ExternalID) })
	return result
}

const editionLabel = `(?:deluxe(?:\s+edition)?|single|ep|(?:[0-9]{4}\s+)?remaster(?:ed)?(?:\s+edition)?)`

var editionSuffix = regexp.MustCompile(`(?i)(?:\s*[（(\[]\s*` + editionLabel + `\s*[)）\]]|\s+(?:[-–—]\s*)?` + editionLabel + `)\s*$`)

const audioFormat = `(?i:flac|mp3|alac|wav|ape|web(?:-dl)?|cd[- ]rip|[0-9]+\s*kbps|[0-9]+\s*bit(?:[ -]+[0-9]+(?:\.[0-9]+)?\s*khz)?|[0-9]+(?:\.[0-9]+)?\s*khz)`

var bracketFormat = regexp.MustCompile(`\[\s*` + audioFormat + `(?:[ /,_-]+` + audioFormat + `)*\s*\]`)
var plainFormat = regexp.MustCompile(`(?:^|\s+)(?:-\s+)?` + audioFormat + `(?:$|\s+)`)

// 零候选放宽只清理独立格式标记，保留标题内部文字。
func cleanSearchFormat(value string) string {
	value = bracketFormat.ReplaceAllString(value, " ")
	for plainFormat.MatchString(value) {
		value = plainFormat.ReplaceAllString(value, " ")
	}
	return metadata.NormalizeValue(strings.TrimSuffix(strings.TrimSpace(value), " -"))
}

func relaxedTitle(title string) string {
	return strings.TrimSpace(editionSuffix.ReplaceAllString(cleanSearchFormat(title), ""))
}

func textSearchEvidence(e catalog.CurationEvidence, mode string, snapshot MatchSnapshot) catalog.CurationEvidence {
	if snapshot.NameOnlyArtist {
		e.ArtistMBID = ""
	}
	if !snapshot.FormatRelaxed {
		return e
	}
	if mode == "single" {
		e.Tracks = slices.Clone(e.Tracks)
		e.Tracks[0].Title = cleanSearchFormat(e.Tracks[0].Title)
		e.Tracks[0].Artist = cleanSearchFormat(e.Tracks[0].Artist)
	} else {
		e.Title = cleanSearchFormat(e.Title)
		e.AlbumArtist = cleanSearchFormat(e.AlbumArtist)
		e.Artist = cleanSearchFormat(e.Artist)
	}
	return e
}

func textSearch(ctx context.Context, e catalog.CurationEvidence, mode string, original, names SearchNames, f sources.ReleaseFinder, rf sources.RecordingFinder, opts MatchOptions, result MatchResult, prior []SearchPage) (MatchResult, error) {
	count := len(e.Tracks)
	if mode == "single" {
		count = 0
	}
	if result.Status == "" {
		var err error
		result, err = queryText(ctx, mode, e, names, count, false, f, rf)
		if err != nil {
			return result, err
		}
		if len(result.Snapshot.Pages) > 0 {
			result.Snapshot.Pages[0].Canonical = canonicalQuery(original, names)
		}
	}
	if len(prior) > 0 {
		if mode == "album" && result.Method == "search" {
			result.Snapshot.Pages = append(result.Snapshot.Pages, prior...)
		} else {
			result.Snapshot.Pages = append(prior, result.Snapshot.Pages...)
		}
	}
	// 每一步先按已有证据判定，只有待定或零候选才继续扩查。
	var decision MatchResult
	decide := func() error {
		result.Snapshot.ArtistID = e.ArtistMBID
		if len(result.Candidates) == 0 {
			decision = result
			return nil
		}
		var err error
		checked := textSearchEvidence(e, mode, result.Snapshot)
		if mode == "single" {
			decision = decideSearchSingle(checked, result)
		} else {
			decision, err = decideSearchAlbum(ctx, checked, result, f, false)
		}
		// 下次查询并入新候选时，复用已经读取的发行详情。
		result.Candidates = decision.Candidates
		return err
	}
	finished := func() bool {
		return decision.Status == "matched" || decision.Status == "no_usable_candidates"
	}
	try := func(n SearchNames, num int, relaxed bool) error {
		m, err := queryText(ctx, mode, e, n, num, relaxed, f, rf)
		if err != nil {
			return err
		}
		result = appendSearch(result, m)
		return decide()
	}
	if err := decide(); err != nil {
		return decision, err
	}
	// 已有艺人对应可能来自同名的另一人。用原名补查时只采信结构。
	triedName := false
	if names.ArtistID != "" && !e.Compilation && original.Artist != "" && original.Title != "" &&
		decision.Status != "matched" && !slices.Contains(decision.Conflicts, "ambiguous") {
		byName := original
		byName.ArtistID = ""
		result.Snapshot.NameOnlyArtist = true
		if err := try(byName, count, true); err != nil {
			return decision, err
		}
		original.ArtistID = ""
		triedName = true
	}
	if finished() {
		return decision, nil
	}
	if !triedName && canonicalQuery(original, names) && !hasExactText(e, mode, result.Candidates) {
		if err := try(original, count, false); err != nil || finished() {
			return decision, err
		}
	}
	if len(result.Candidates) == 0 && original.Title != "" {
		relaxed := original
		relaxed.Title = relaxedTitle(original.Title)
		relaxed.Artist = cleanSearchFormat(original.Artist)
		result.Snapshot.FormatRelaxed = cleanSearchFormat(original.Title) != original.Title || relaxed.Artist != original.Artist
		if relaxed.Title != "" && relaxed != original {
			if err := try(relaxed, count, true); err != nil || finished() {
				return decision, err
			}
		}
		if len(result.Candidates) == 0 && count > 0 {
			if err := try(relaxed, 0, true); err != nil || finished() {
				return decision, err
			}
		}
	}
	exists := e.ArtistMBID != ""
	if e.ArtistMBID == "" && opts.Artists != nil && original.Artist != "" && !hasExactText(e, mode, result.Candidates) {
		artists, page, unknown, err := lookupArtist(ctx, opts.Artists, original.Artist)
		if err != nil {
			return result, err
		}
		result.Snapshot.Pages = append(result.Snapshot.Pages, page)
		result.Snapshot.Artists, result.ArtistEvidence = artists, artists
		exists = len(artists) > 0 || unknown
		if len(artists) == 1 && !unknown {
			for id := range artists {
				e.ArtistMBID = id
			}
			if err := decide(); err != nil || finished() {
				return decision, err
			}
			if !hasExactText(e, mode, result.Candidates) && !(names.ArtistID == e.ArtistMBID && names.Title == original.Title) {
				n := original
				n.ArtistID = e.ArtistMBID
				if err := try(n, count, false); err != nil {
					return decision, err
				}
			}
		}
	}
	decision.Snapshot, decision.ArtistEvidence = result.Snapshot, result.ArtistEvidence
	if len(result.Candidates) == 0 {
		if exists || opts.Artists == nil || original.Artist == "" {
			decision.Route = "lookup"
		} else {
			decision.Route = "web"
		}
	}
	return decision, nil
}
func hasExactText(e catalog.CurationEvidence, mode string, rows []Candidate) bool {
	n := localNames(e, mode)
	counts := localDiscCounts(e)
	for _, c := range rows {
		var cs []sources.Credit
		if c.Release != nil {
			cs = c.Release.Artists
		}
		if c.Recording != nil {
			cs = c.Recording.Artists
		}
		if c.Release != nil {
			if wrong, _ := searchShape(counts, len(e.Tracks), c); wrong {
				continue
			}
		}
		if sameText(n.Title, c.Title) && (sameArtist(n.Artist, c.Artist) || creditID(cs, e.ArtistMBID)) {
			return true
		}
	}
	return false
}
func lookupArtist(ctx context.Context, f sources.ArtistFinder, name string) (map[string]sources.Artist, SearchPage, bool, error) {
	var rows []sources.Artist
	var err error
	p := SearchPage{Kind: "artist"}
	if pf, ok := f.(curation.ArtistPageFinder); ok {
		page, ferr := found(pf.SearchArtistPage(ctx, name))
		rows, err, p.PageInfo = page.Items, ferr, page.PageInfo
	} else {
		rows, err = found(f.SearchArtists(ctx, name))
	}
	hits := map[string]sources.Artist{}
	for _, a := range rows {
		p.IDs = append(p.IDs, a.ID)
		if artistNamed(a, name) {
			hits[a.ID] = a
		}
	}
	return hits, p, !p.Complete && (len(rows) > 0 || p.Count != nil && *p.Count > 0), err
}
func artistNamed(a sources.Artist, name string) bool {
	return sameArtist(a.Name, name) || slices.ContainsFunc(a.Aliases, func(alias sources.Alias) bool { return sameArtist(alias.Name, name) })
}
func checkArtistNames(ctx context.Context, e catalog.CurationEvidence, rows []sources.Release, f sources.ArtistFinder) (catalog.CurationEvidence, map[string]sources.Artist, error) {
	if f == nil || e.Compilation || e.ArtistMBID != "" || e.ArtistPlaceholder {
		return e, nil, nil
	}
	name := albumArtist(e)
	known := map[string]sources.Artist{}
	matches := map[string]bool{}
	for _, r := range rows {
		if len(r.Artists) != 1 {
			continue
		}
		credit := r.Artists[0]
		if credit.ArtistID == "" {
			continue
		}
		if sameArtist(name, credit.Name) {
			matches[credit.ArtistID] = true
			continue
		}
		a, ok := known[credit.ArtistID]
		if !ok {
			var err error
			a, err = found(f.ArtistByID(ctx, credit.ArtistID))
			if err != nil {
				return e, nil, err
			}
			known[credit.ArtistID] = a
		}
		if artistNamed(a, name) {
			matches[credit.ArtistID] = true
		}
	}
	if len(matches) == 1 {
		for id := range matches {
			e.ArtistMBID = id
		}
	}
	return e, known, nil
}
