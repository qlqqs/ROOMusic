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
	Artists sources.ArtistFinder
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
	CountRelaxed         bool                       `json:"count_relaxed,omitempty"`
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
	names := localNames(searchInput, mode)
	hasID := searchIdentifier(searchInput, mode)
	completeNames := names.Title != "" && (names.Artist != "" || e.Compilation && mode == "album")
	emptyNames := names.Title == "" && names.Artist == ""
	var prior []SearchPage
	if hasID {
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
						agree, err := tagArtistNamesAgree(ctx, r, albumArtist(e), "", opts.Artists, artists)
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
			discs, err := discCandidates(ctx, f, e)
			if err != nil {
				return MatchResult{}, err
			}
			if discs.hit {
				discs.pages = prior
				return checkCandidates(ctx, f, e, "disc_id", discs, opts)
			}
			if pf, ok := f.(curation.ReleasePageFinder); ok {
				for _, q := range []curation.ReleaseSearch{
					{Barcodes: e.SearchBarcodes()},
					{CatalogNumber: e.Authenticity["catalog_number"].SearchValue(e.CatalogNumber), Label: e.Authenticity["label"].SearchValue(e.Label)},
				} {
					if len(q.Barcodes) == 0 && q.CatalogNumber == "" {
						continue
					}
					page, err := found(pf.SearchReleasePage(ctx, q))
					if err != nil {
						return MatchResult{}, err
					}
					ev := albumPageResult(page, false).Snapshot.Pages[0]
					method := "catalog_number"
					if len(q.Barcodes) > 0 {
						method = "barcode"
					}
					ev.Kind = method
					prior = append(prior, ev)
					if len(page.Items) == 0 {
						continue
					}
					list := releaseList{items: uniqueReleases(page.Items), pages: prior, counts: page.Counts}
					if !page.Complete {
						list.codes = []string{"list_truncated"}
					}
					return checkCandidates(ctx, f, e, method, list, opts)
				}
			} else if m, err := legacyAlbumIDs(ctx, e, f); err != nil || m.Status != "" {
				return m, err
			}
			isrcs, err := isrcCandidates(ctx, rf, e)
			if err != nil {
				return MatchResult{}, err
			}
			prior = append(prior, isrcs.pages...)
			if isrcs.hit {
				isrcs.pages = prior
				return checkCandidates(ctx, f, e, "isrc", isrcs, opts)
			}

		}
	}
	fingerprint := "unavailable"
	var queriedPrint bool
	if fp != nil && (hasID || completeNames || emptyNames) {
		m, state, err := fp.match(ctx, mode, e, f, rf)
		fingerprint = state
		queriedPrint = state == "used" && !m.noFingerprintQuery
		if m.Status != "" || err != nil {
			m.Snapshot.Pages = append(prior, m.Snapshot.Pages...)
			m.Fingerprint = state
			return m, err
		}
	}
	if hasID || !completeNames {
		m := newMatch(mode, "")
		m.Fingerprint = fingerprint
		m.Snapshot.Pages = prior
		m.Route = "lookup"
		if !hasID && (!emptyNames || !queriedPrint) {
			m.Route = ""
			m.Conflicts = []string{"insufficient_evidence"}
		}
		return m, nil
	}
	if !e.Compilation {
		names.ArtistID = ""
	}
	m, err := textSearch(ctx, searchInput, mode, names, f, rf, opts.Artists)

	m.Fingerprint = fingerprint
	return m, err
}

func releaseAdmission(e catalog.CurationEvidence, fingerprints bool) (allowed, hasID bool) {
	e.Compilation = curation.CompilationEvidence(e)
	e = curation.LocalSearchEvidence(e)
	mode := "album"
	if e.CandidateKind == "loose_unknown" {
		if len(e.Tracks) != 1 {
			return false, false
		}
		mode = "single"
	}
	n := localNames(e, mode)
	hasID = searchIdentifier(e, mode)
	return e.CanMatch(n.Title, n.Artist, hasID, fingerprints), hasID
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
func searchIdentifier(e catalog.CurationEvidence, mode string) bool {
	if len(sampleISRCTracks(e)) > 0 {
		return true
	}
	if mode == "single" {
		return len(tags(e.Tracks, "mb_recording")) > 0
	}
	return len(tags(e.Tracks, "mb_release")) > 0 || len(e.DiscTOCs()) > 0 ||
		len(e.SearchBarcodes()) > 0 || e.Authenticity["catalog_number"].SearchValue(e.CatalogNumber) != ""
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
		q := curation.ReleaseSearch{Title: n.Title, Artist: n.Artist, ArtistID: n.ArtistID, TrackCount: count}
		if q.Title == "" && q.ArtistID == "" {
			return newMatch(mode, ""), nil
		}
		if pf, ok := f.(curation.ReleasePageFinder); ok {
			p, err := found(pf.SearchReleasePage(ctx, q))
			return albumPageResult(p, relaxed), err
		}
		rows, err := found(f.SearchReleases(ctx, sources.ReleaseQuery{Title: q.Title, Artist: q.Artist, TrackCount: q.TrackCount}))
		return albumPageResult(curation.ReleasePage{Items: rows}, relaxed), err
	}
	if n.ArtistID == "" && (n.Title == "" || n.Artist == "") {
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

const editionLabel = `(?:豪华版|豪華版|deluxe(?:\s+edition)?|single|ep|(?:[0-9]{4}\s+)?remaster(?:ed)?(?:\s+edition)?)`

var editionSuffix = regexp.MustCompile(`(?i)(?:\s*[（(\[]\s*` + editionLabel + `\s*[)）\]]|\s+(?:[-–—]\s*)?` + editionLabel + `)\s*$`)

const audioFormat = `(?i:flac|mp3|alac|wav|ape|web(?:-dl)?|cd[- ]rip|[0-9]+\s*k(?:bps)?|[0-9]+\s*bit(?:[ -]+[0-9]+(?:\.[0-9]+)?\s*khz)?|[0-9]+(?:\.[0-9]+)?\s*khz)`

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
		e.Tracks[0].Title = relaxedTitle(e.Tracks[0].Title)
		e.Tracks[0].Artist = cleanSearchFormat(e.Tracks[0].Artist)
	}
	// 专辑查找可放宽 query，组和部分结构仍用原专辑名、艺人名核对。
	return e
}

func textSearch(ctx context.Context, e catalog.CurationEvidence, mode string, names SearchNames, f sources.ReleaseFinder, rf sources.RecordingFinder, af sources.ArtistFinder) (MatchResult, error) {
	count := len(e.Tracks)
	if mode == "single" {
		count = 0
	}
	var pages []SearchPage
	query := func(n SearchNames, num int, relaxed bool) (MatchResult, error) {
		m, err := queryText(ctx, mode, e, n, num, relaxed, f, rf)
		if err != nil {
			return m, err
		}
		pages = append(pages, m.Snapshot.Pages...)
		m.Snapshot.Pages = slices.Clone(pages)
		m.Snapshot.ArtistID = e.ArtistMBID
		m.Snapshot.FormatRelaxed = relaxed
		if len(m.Candidates) == 0 {
			return m, nil
		}
		input := textSearchEvidence(e, mode, m.Snapshot)
		if mode == "single" {
			return decideSearchSingle(input, m), nil
		}
		return decideSearchAlbum(ctx, input, m, &searchDetails{finder: f, attempted: map[string]bool{}})
	}
	m, err := query(names, count, false)
	if err != nil || len(m.Candidates) > 0 {
		return m, err
	}
	relaxed := names
	relaxed.Title = relaxedTitle(names.Title)
	relaxed.Artist = cleanSearchFormat(names.Artist)
	if relaxed.Title != "" && (relaxed != names || count != 0) {
		m, err = query(relaxed, 0, true)
		if err != nil || len(m.Candidates) > 0 {
			return m, err
		}
	}
	_, albumPages := f.(curation.ReleasePageFinder)
	_, singlePages := rf.(curation.RecordingPageFinder)
	if af != nil && names.Artist != "" && (mode == "album" && albumPages || mode == "single" && singlePages) {
		artists, page, unknown, err := lookupArtist(ctx, af, names.Artist)
		if err != nil {
			return m, err
		}
		pages = append(pages, page)
		m.Snapshot.Pages = pages
		if len(artists) == 1 && !unknown {
			for id := range artists {
				e.ArtistMBID = id
			}
			m, err = query(SearchNames{ArtistID: e.ArtistMBID}, count, false)
			m.ArtistEvidence, m.Snapshot.Artists = artists, artists
			if err != nil || len(m.Candidates) > 0 {
				return m, err
			}
		}
	}
	m.Route = "lookup"
	return m, nil
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
