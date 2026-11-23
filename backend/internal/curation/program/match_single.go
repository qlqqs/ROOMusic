package program

import (
	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// DecideSingle 先按时长筛选；同一 ISRC 出现多个时长合适的录音时不能靠标题消歧。
func DecideSingle(e catalog.CurationEvidence, method string, recordings []sources.Recording) MatchResult {
	result := newMatch("single", method)
	result.Candidates = recordingCandidates(method, recordings)
	if len(recordings) == 0 {
		return result
	}
	if method == "search" {
		return uncertain(result, "search_only")
	}
	if len(e.Tracks) != 1 {
		return uncertain(result, "track_count")
	}
	t := e.Tracks[0]
	var selected *sources.Recording
	knownLengths := false
	for i := range recordings {
		known, match := compareLength(t.DurationSeconds, recordings[i].LengthMS)
		knownLengths = knownLengths || known
		if !match {
			continue
		}
		if selected != nil {
			return uncertain(result, "ambiguous")
		}
		selected = &recordings[i]
	}
	if selected == nil {
		if knownLengths {
			return uncertain(result, "duration_mismatch")
		}
		return uncertain(result)
	}
	result.Conflicts = append(result.Conflicts, linkConflicts(e, "track", t.ID, t.ExternalID, selected.ID)...)
	if _, mismatch := compareISRCs(trackISRCs(t), selected.ISRCs); mismatch {
		result.Conflicts = append(result.Conflicts, "isrc_mismatch")
	}
	if method == "acoustid" && !t.TitlePlaceholder && !sameText(t.Title, selected.Title) {
		result.Conflicts = append(result.Conflicts, "title_mismatch")
	}
	if len(result.Conflicts) > 0 || (method == "isrc" && (t.TitlePlaceholder || !sameText(t.Title, selected.Title))) {
		return uncertain(result)
	}
	result.Status, result.Recording = "matched", selected
	result.Pairs = []TrackPair{{TrackID: t.ID, RecordingID: selected.ID}}
	return result
}

func recordingCandidates(method string, recordings []sources.Recording) []Candidate {
	rows := recordings[:min(10, len(recordings))]
	out := make([]Candidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, Candidate{Source: "musicbrainz", ExternalID: r.ID, Method: method, Title: r.Title, Artist: credits(r.Artists), CatalogNumbers: []string{}})
	}
	return out
}
