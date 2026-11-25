package program

import (
	"cmp"
	"maps"
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type TrackPair struct {
	TrackID, RecordingID string
}

type MatchResult struct {
	Level, Rule, GroupID, Route, RepresentativeID string
	Credits                                       []sources.Credit
	TrackTitles                                   map[string]string
	Snapshot                                      MatchSnapshot
	Offline                                       bool
	ArtistEvidence                                map[string]sources.Artist
	Mode, Method, Status, Fingerprint             string
	Conflicts                                     []string
	Candidates                                    []Candidate
	Release                                       *sources.Release
	Recording                                     *sources.Recording
	Pairs                                         []TrackPair
}

type Candidate struct {
	Release        *sources.Release        `json:"release,omitempty"`
	Recording      *sources.Recording      `json:"recording,omitempty"`
	Counts         *curation.ReleaseCounts `json:"counts,omitempty"`
	Conflicts      []string                `json:"conflicts,omitempty"`
	TextEvidence   *TagTextEvidence        `json:"tag_text,omitempty"`
	Score          float64                 `json:"score,omitempty"`
	Source         string                  `json:"source"`
	ExternalID     string                  `json:"external_id"`
	Method         string                  `json:"method"`
	Title          string                  `json:"title"`
	Artist         string                  `json:"artist"`
	Date           string                  `json:"date"`
	Country        string                  `json:"country"`
	Status         string                  `json:"status"`
	TrackCount     int                     `json:"track_count"`
	Barcode        string                  `json:"barcode"`
	CatalogNumbers []string                `json:"catalog_numbers"`
	ReleaseGroupID string                  `json:"release_group_id"`
}

func newMatch(mode, method string) MatchResult {
	return MatchResult{Mode: mode, Method: method, Status: "no_match", Fingerprint: "not_reached", Conflicts: []string{}, Candidates: []Candidate{}}
}

func uncertain(result MatchResult, codes ...string) MatchResult {
	result.Status = "uncertain"
	if len(result.Candidates) > 0 {
		result.Route = "select"
	}
	result.Conflicts = append(result.Conflicts, codes...)
	return result
}

func credits(names []sources.Credit) string {
	var b strings.Builder
	for _, c := range names {
		b.WriteString(c.Name)
		b.WriteString(c.JoinPhrase)
	}
	return b.String()
}

func looseKey(s string) string  { return textnorm.Loose(textnorm.Text(s)) }
func sameText(a, b string) bool { key := looseKey(a); return key != "" && key == looseKey(b) }

func albumArtist(e catalog.CurationEvidence) string {
	if e.AlbumArtist != "" {
		return e.AlbumArtist
	}
	return e.Artist
}

func isrcKey(raw string) string {
	return strings.ToUpper(strings.Map(func(r rune) rune {
		if r == '-' || unicode.IsSpace(r) {
			return -1
		}
		return r
	}, raw))
}

func trackISRCs(t catalog.CurationTrack) []string {
	keys := map[string]bool{}
	for _, raw := range append(slices.Clone(t.Identifiers["isrc"]), t.CueISRC) {
		if key := isrcKey(raw); key != "" {
			keys[key] = true
		}
	}
	return slices.Sorted(maps.Keys(keys))
}

func tags(tracks []catalog.CurationTrack, kind string) []string {
	keys := map[string]bool{}
	for _, t := range tracks {
		for _, v := range t.Identifiers[kind] {
			if v != "" {
				keys[v] = true
			}
		}
	}
	return slices.Sorted(maps.Keys(keys))
}

func DiscTOCs(e catalog.CurationEvidence) []sources.DiscTOC { return e.DiscTOCs() }

func linkConflicts(e catalog.CurationEvidence, objectType, id, existing, candidate string) []string {
	var codes []string
	if hasMarker(e.Suppressions, objectType, id, "external_link", "musicbrainz\x00"+candidate) {
		codes = append(codes, "suppressed")
	}
	if existing != "" && existing != candidate {
		codes = append(codes, "existing_link")
	}
	return codes
}

func compareLength(local int, ms *int64) (comparable, matches bool) {
	if local <= 0 || ms == nil || *ms <= 0 {
		return false, false
	}
	seconds := float64(*ms) / 1000
	return true, math.Abs(float64(local)-seconds) <= math.Max(3, seconds*0.01)
}

func releaseCandidates(method string, releases []sources.Release) []Candidate {
	rows := slices.Clone(releases[:min(100, len(releases))])
	slices.SortFunc(rows, func(a, b sources.Release) int { return cmp.Compare(a.ID, b.ID) })
	out := make([]Candidate, 0, len(rows))
	for _, r := range rows {
		count := 0
		for _, m := range r.Media {
			count += max(m.TrackCount, len(m.Tracks))
		}
		numbers := []string{}
		for _, l := range r.Labels {
			if l.CatalogNumber != "" && !slices.Contains(numbers, l.CatalogNumber) {
				numbers = append(numbers, l.CatalogNumber)
			}
		}
		slices.Sort(numbers)
		out = append(out, Candidate{Release: &r, Source: "musicbrainz", ExternalID: r.ID, Method: method, Title: r.Title, Artist: credits(r.Artists), Date: r.Date, Country: r.Country, Status: r.Status, TrackCount: count, Barcode: r.Barcode, CatalogNumbers: numbers, ReleaseGroupID: r.Group.ID})
	}
	return out
}

// PendingEvidence 保留能供后续核对使用的本地元数据与位置，不保存文件路径。
func PendingEvidence(e catalog.CurationEvidence) map[string]any {
	return pendingMatchEvidence(e, "")
}

func pendingMatchEvidence(e catalog.CurationEvidence, method string) map[string]any {
	type track struct {
		Authenticity     map[string]metadata.Authenticity `json:"authenticity"`
		ID               string                           `json:"id"`
		Title            string                           `json:"title"`
		Artist           string                           `json:"artist"`
		Disc             int                              `json:"disc"`
		Position         int                              `json:"position"`
		PositionInferred bool                             `json:"position_inferred"`
		Duration         int                              `json:"duration_seconds"`
		Identifiers      map[string][]string              `json:"identifiers"`
		ISRCs            []string                         `json:"isrcs"`
	}
	discCounts := map[int]int{}
	for _, d := range e.Media {
		discCounts[d] = 0
	}
	rows := make([]track, 0, len(e.Tracks))
	for _, t := range e.SortedTracks() {
		discCounts[t.Disc]++
		rows = append(rows, track{t.Authenticity, t.ID, t.Title, t.Artist, t.Disc, t.Position, t.PositionInferred, t.DurationSeconds, t.Identifiers, trackISRCs(t)})
	}
	auth := maps.Clone(e.Authenticity)
	if auth == nil {
		auth = map[string]metadata.Authenticity{}
	}
	auth["artist"] = e.SelectedArtistAuth()
	usesInferred := false
	switch method {
	case "search":
		title, artist := auth["title"], auth["artist"]
		if e.CandidateKind == "loose_unknown" && len(e.Tracks) == 1 {
			title, artist = e.Tracks[0].Authenticity["title"], e.Tracks[0].Authenticity["artist"]
		}
		usesInferred = title == metadata.Inferred || artist == metadata.Inferred
	case "catalog_number":
		usesInferred = auth["catalog_number"] == metadata.Inferred || auth["label"] == metadata.Inferred
	case "barcode":
		usesInferred = slices.Contains(e.BarcodeAuthenticity, metadata.Inferred)
	}
	return map[string]any{
		"title": e.Title, "artist": albumArtist(e), "album_artist": e.AlbumArtist, "year": e.Year,
		"barcodes": e.Barcodes, "catalog_number": e.CatalogNumber, "label": e.Label,
		"genre": e.Genres, "edition": e.Edition, "media_type": e.MediaType,
		"disc_count": len(discCounts), "track_counts": discCounts, "tracks": rows,
		"authenticity": auth, "barcode_authenticity": e.BarcodeAuthenticity, "uses_inferred": usesInferred,
	}
}

// 选项只含可以采纳的对应，抑制记录仍保存在匹配快照中。
func (m MatchResult) modelCandidates() []Candidate {
	out := make([]Candidate, 0, len(m.Candidates))
	for _, c := range m.Candidates {
		if !blockedCodes(c.Conflicts) {
			out = append(out, c)
		}
	}
	return out
}
func (m MatchResult) savedSnapshot() MatchSnapshot {
	snapshot := m.Snapshot
	for _, c := range m.Candidates {
		if blockedCodes(c.Conflicts) {
			snapshot.Blocked = append(snapshot.Blocked, c)
		}
	}
	return snapshot
}
