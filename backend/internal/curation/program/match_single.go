package program

import (
	"cmp"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/metadata"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// DecideSingle 先按时长筛选；同一 ISRC 出现多个时长合适的录音时不能靠标题消歧。
func DecideSingle(e catalog.CurationEvidence, method string, recordings []sources.Recording) MatchResult {
	result := newMatch("single", method)
	result.Candidates = recordingCandidates(method, recordings)
	result.Snapshot = MatchSnapshot{Version: 1, Method: method}
	if len(recordings) == 0 {
		return result
	}
	if len(e.Tracks) != 1 {
		return uncertain(result, "track_count")
	}
	t := e.Tracks[0]
	if len(recordings) > 100 {
		markBlocked(e, result.Mode, result.Candidates)
		return uncertain(result, "list_truncated")
	}
	result.Snapshot.CandidateSetComplete = true
	if method == "search" {
		return decideRecordingText(e, result)
	}
	var selected *sources.Recording
	knownLengths := false
	valid := 0
	for i := range result.Candidates {
		candidate := &result.Candidates[i]
		candidate.Conflicts = linkConflicts(e, "track", t.ID, t.ExternalID, candidate.ExternalID)
		if blockedCodes(candidate.Conflicts) {
			continue
		}
		valid++
		r := candidate.Recording
		known, match := compareLength(t.DurationSeconds, r.LengthMS)
		knownLengths = knownLengths || known
		if !match {
			if known {
				candidate.Conflicts = append(candidate.Conflicts, "duration_mismatch")
			}
			continue
		}
		if selected != nil {
			result = uncertain(result, "ambiguous")
			continue
		}
		selected = r
	}
	if valid == 0 {
		result.Status, result.Conflicts = "no_usable_candidates", candidateConflicts(result.Candidates)
		return result
	}
	if result.Status == "uncertain" {
		return result
	}
	if selected == nil {
		if knownLengths {
			return uncertain(result, "duration_mismatch")
		}
		return uncertain(result)
	}
	if _, mismatch := compareISRCs(trackISRCs(t), selected.ISRCs); mismatch {
		result.Conflicts = append(result.Conflicts, "isrc_mismatch")
	}
	if method == "acoustid" && !t.TitlePlaceholder && !sameText(t.Title, selected.Title) {
		result.Conflicts = append(result.Conflicts, "title_mismatch")
	}
	if len(result.Conflicts) > 0 || (method == "isrc" && (t.TitlePlaceholder || !sameText(t.Title, selected.Title))) {
		result = uncertain(result)
		return result
	}
	result.Status, result.Recording = "matched", selected
	result.Level, result.Rule = "recording", "identifier"
	result.Pairs = []TrackPair{{TrackID: t.ID, RecordingID: selected.ID}}
	return result
}

func recordingCandidates(method string, recordings []sources.Recording) []Candidate {
	rows := slices.Clone(recordings[:min(100, len(recordings))])
	slices.SortFunc(rows, func(a, b sources.Recording) int { return cmp.Compare(a.ID, b.ID) })
	out := make([]Candidate, 0, len(rows))
	for _, r := range rows {
		r.Releases = nil
		out = append(out, Candidate{Recording: &r, Source: "musicbrainz", ExternalID: r.ID, Method: method, Title: r.Title, Artist: credits(r.Artists), CatalogNumbers: []string{}})
	}
	return out
}

func decideRecordingText(e catalog.CurationEvidence, result MatchResult) MatchResult {
	t := e.Tracks[0]
	var passing []*sources.Recording
	freq := map[string]int{}
	blocked := 0
	for i := range result.Candidates {
		candidate := &result.Candidates[i]
		r := candidate.Recording
		codes := linkConflicts(e, "track", t.ID, t.ExternalID, r.ID)
		if !sameText(t.Title, r.Title) {
			codes = append(codes, "title_mismatch")
		}
		if !sameText(t.Artist, credits(r.Artists)) && !creditID(r.Artists, e.ArtistMBID) {
			codes = append(codes, "artist_mismatch")
		}
		if known, match := compareLength(t.DurationSeconds, r.LengthMS); !known {
			codes = append(codes, "duration_missing")
		} else if !match {
			codes = append(codes, "duration_mismatch")
		}
		if ids := t.Identifiers["mb_recording"]; len(ids) > 0 && !slices.Contains(ids, r.ID) {
			codes = append(codes, "recording_tag_mismatch")
		}
		if _, bad := compareISRCs(trackISRCs(t), r.ISRCs); bad {
			codes = append(codes, "isrc_mismatch")
		}
		candidate.Conflicts = codes
		if len(codes) == 0 {
			passing = append(passing, r)
		}
		if blockedCodes(codes) {
			blocked++
		}
		for _, code := range codes {
			freq[code]++
		}
	}
	switch len(passing) {
	case 0:
		result = uncertain(result, commonConflicts(freq)...)
		if blocked == len(result.Candidates) {
			result.Status, result.Route = "no_usable_candidates", ""
		}
	case 1:
		result.Status, result.Level, result.Rule = "matched", "recording", "recording_text"
		result.Recording = passing[0]
		result.Pairs = []TrackPair{{t.ID, passing[0].ID}}
	default:
		cs := passing[0].Artists
		if t.Authenticity["title"] != metadata.Real || t.Authenticity["artist"] != metadata.Real || len(cs) == 0 {
			return uncertain(result, "ambiguous")
		}
		for _, r := range passing {
			if !sameCredits(cs, r.Artists) {
				return uncertain(result, "ambiguous")
			}
		}
		result.Status, result.Level, result.Rule, result.Credits = "matched", "artist", "common_credit", cs
	}
	return result
}
