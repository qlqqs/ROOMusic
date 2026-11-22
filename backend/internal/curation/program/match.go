package program

import (
	"cmp"
	"maps"
	"math"
	"slices"
	"strings"
	"unicode"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type TrackPair struct {
	TrackID, RecordingID string
}

type MatchResult struct {
	Mode, Method, Status string
	Conflicts            []string
	Candidates           []Candidate
	Release              *sources.Release
	Recording            *sources.Recording
	Pairs                []TrackPair
}

type Candidate struct {
	Source         string   `json:"source"`
	ExternalID     string   `json:"external_id"`
	Method         string   `json:"method"`
	Title          string   `json:"title"`
	Artist         string   `json:"artist"`
	Date           string   `json:"date"`
	Country        string   `json:"country"`
	Status         string   `json:"status"`
	TrackCount     int      `json:"track_count"`
	Barcode        string   `json:"barcode"`
	CatalogNumbers []string `json:"catalog_numbers"`
	ReleaseGroupID string   `json:"release_group_id"`
}

func newMatch(mode, method string) MatchResult {
	return MatchResult{Mode: mode, Method: method, Status: "no_match", Conflicts: []string{}, Candidates: []Candidate{}}
}

func uncertain(result MatchResult, codes ...string) MatchResult {
	result.Status = "uncertain"
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

func sortedTracks(e catalog.CurationEvidence) []catalog.CurationTrack {
	rows := slices.Clone(e.Tracks)
	slices.SortFunc(rows, func(a, b catalog.CurationTrack) int {
		return cmp.Or(cmp.Compare(a.Disc, b.Disc), cmp.Compare(a.Position, b.Position), cmp.Compare(a.ID, b.ID))
	})
	return rows
}

func discPositions(e catalog.CurationEvidence) []int {
	discs := slices.Clone(e.Media)
	for _, t := range e.Tracks {
		discs = append(discs, t.Disc)
	}
	slices.Sort(discs)
	return slices.Compact(discs)
}

// DiscTOCs 只有所有介质都能从同一镜像的连续 CUE 索引重建时才返回目录。
func DiscTOCs(e catalog.CurationEvidence) []sources.DiscTOC {
	byDisc := map[int][]catalog.CurationTrack{}
	for _, t := range sortedTracks(e) {
		byDisc[t.Disc] = append(byDisc[t.Disc], t)
	}
	var out []sources.DiscTOC
	for _, d := range discPositions(e) {
		rows := byDisc[d]
		if len(rows) == 0 || len(rows) > 99 {
			return nil
		}
		first, last := rows[0], rows[len(rows)-1]
		if first.ParentFile == "" || first.CueSheetPath == "" || last.CueEndFrames == nil {
			return nil
		}
		toc := sources.DiscTOC{}
		prev := -1
		for i, t := range rows {
			if t.SourceKind != "cue_virtual" || t.SourceRootID != first.SourceRootID || t.CueSheetPath != first.CueSheetPath || t.ParentFile != first.ParentFile || t.Position != i+1 || t.CueIndexFrames == nil || *t.CueIndexFrames <= prev {
				return nil
			}
			prev = *t.CueIndexFrames
			toc.Offsets = append(toc.Offsets, 150+prev)
		}
		if *last.CueEndFrames <= prev || int64(*last.CueEndFrames) > math.MaxUint32-150 {
			return nil
		}
		toc.Leadout = 150 + *last.CueEndFrames
		out = append(out, toc)
	}
	return out
}

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
	out := make([]Candidate, 0, min(10, len(releases)))
	for _, r := range releases[:min(10, len(releases))] {
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
		out = append(out, Candidate{Source: "musicbrainz", ExternalID: r.ID, Method: method, Title: r.Title, Artist: credits(r.Artists), Date: r.Date, Country: r.Country, Status: r.Status, TrackCount: count, Barcode: r.Barcode, CatalogNumbers: numbers, ReleaseGroupID: r.Group.ID})
	}
	return out
}

// PendingEvidence 保留能供后续核对使用的本地元数据与位置，不保存文件路径。
func PendingEvidence(e catalog.CurationEvidence) map[string]any {
	type track struct {
		ID               string              `json:"id"`
		Title            string              `json:"title"`
		Artist           string              `json:"artist"`
		Disc             int                 `json:"disc"`
		Position         int                 `json:"position"`
		PositionInferred bool                `json:"position_inferred"`
		Duration         int                 `json:"duration_seconds"`
		Identifiers      map[string][]string `json:"identifiers"`
		ISRCs            []string            `json:"isrcs"`
	}
	discCounts := map[int]int{}
	for _, d := range e.Media {
		discCounts[d] = 0
	}
	rows := make([]track, 0, len(e.Tracks))
	for _, t := range sortedTracks(e) {
		discCounts[t.Disc]++
		rows = append(rows, track{t.ID, t.Title, t.Artist, t.Disc, t.Position, t.PositionInferred, t.DurationSeconds, t.Identifiers, trackISRCs(t)})
	}
	return map[string]any{"title": e.Title, "artist": albumArtist(e), "barcodes": e.Barcodes, "catalog_number": e.CatalogNumber, "label": e.Label, "disc_count": len(discCounts), "track_counts": discCounts, "tracks": rows}
}
