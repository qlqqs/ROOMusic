package program

import (
	"context"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type FingerprintStatus int

const (
	FingerprintOK FingerprintStatus = iota
	FingerprintPermanent
	FingerprintTransient
)

type TrackFingerprint struct {
	Value           string
	DurationSeconds int
}

type Fingerprints interface {
	Fingerprint(ctx context.Context, trackID string) (TrackFingerprint, FingerprintStatus, error)
}

type FingerprintStep struct {
	Finder sources.FingerprintFinder
	Prints Fingerprints
}

var errLocalPrint = errors.New("local_fingerprint_failed")

type sampleFailure struct {
	source string
	err    error
}

func (e *sampleFailure) Error() string { return e.source + ": " + e.err.Error() }
func (e *sampleFailure) Unwrap() error { return e.err }

type sampledFingerprint struct {
	trackID string
	print   TrackFingerprint
}

func (fp *FingerprintStep) sample(ctx context.Context, tracks []catalog.CurationTrack) ([]sampledFingerprint, error) {
	var prints []sampledFingerprint
	var transient bool

	for _, track := range tracks {
		print, status, err := fp.Prints.Fingerprint(ctx, track.ID)
		if err != nil {
			return nil, err
		}
		switch status {
		case FingerprintOK:
			prints = append(prints, sampledFingerprint{track.ID, print})
		case FingerprintTransient:
			transient = true
		}
	}
	if 2*len(prints) < len(tracks) {
		if transient {
			return nil, sources.ErrTryLater
		}
		return nil, nil
	}
	return prints, nil
}

type fingerprintHit struct {
	trackID    string
	recordings []sources.FingerprintRecording
	scores     map[string]float64
}

func (fp *FingerprintStep) match(ctx context.Context, mode string, e catalog.CurationEvidence, f sources.ReleaseFinder, rf sources.RecordingFinder) (MatchResult, string, error) {
	tracks := sampleFour(e.SortedTracks())
	var hits []fingerprintHit
	var queried bool
	var err error
	if a := currentAttempt(ctx); a != nil {
		hits, queried, err = a.worker.queuedHits(ctx, a.item, tracks)
	} else {
		var prints []sampledFingerprint
		prints, err = fp.sample(ctx, tracks)
		if err == nil {
			queried = len(prints) > 0
			for _, sample := range prints {
				var hit fingerprintHit
				hit, err = fp.lookup(ctx, sample.trackID, sample.print)
				if err != nil {
					break
				}
				if len(hit.recordings) > 0 {
					hits = append(hits, hit)
				}
			}
		}
	}
	if errors.Is(err, sources.ErrInvalidKey) || failureCode(err) == "source_disabled" {
		return MatchResult{}, "unavailable", nil
	}
	if err != nil {
		return MatchResult{}, "used", err
	}
	if len(hits) == 0 {
		return MatchResult{noFingerprintQuery: !queried}, "used", nil
	}
	var result MatchResult
	if mode == "single" {
		result, err = fingerprintSingle(ctx, e, rf, hits[0])
	} else {
		list, scores := fingerprintReleases(e, hits, len(tracks))
		result, err = fullCandidates(ctx, f, e, "acoustid", list)
		result = scoreCandidates(result, scores)
	}
	return result, "used", err
}

func (fp *FingerprintStep) lookup(ctx context.Context, id string, print TrackFingerprint) (fingerprintHit, error) {
	matches, err := queryResult(fp.Finder.LookupFingerprint(ctx, sources.Fingerprint(print)))
	if err != nil {
		return fingerprintHit{}, &sampleFailure{"acoustid", sourceFailure(ctx, "acoustid", err)}
	}
	hit := fingerprintHit{trackID: id, scores: make(map[string]float64)}
	for _, m := range matches {
		if m.Score < 0.9 {
			continue
		}
		for _, r := range m.Recordings {
			if r.ID == "" {
				continue
			}
			if _, ok := hit.scores[r.ID]; !ok {
				hit.recordings = append(hit.recordings, r)
			}
			hit.scores[r.ID] = max(hit.scores[r.ID], m.Score)
		}
	}
	return hit, nil
}

func fingerprintReleases(e catalog.CurationEvidence, hits []fingerprintHit, sampled int) (releaseList, map[string]float64) {
	discs := len(e.DiscPositions())
	list := releaseList{hit: true, counts: map[string]curation.ReleaseCounts{}, recordingIDs: make(map[string]map[string]bool)}
	var pages [][]sources.Release
	trackScores := make([]map[string]float64, 0, len(hits))
	for _, hit := range hits {
		recordings := make(map[string]bool)
		scores := make(map[string]float64)
		var page []sources.Release
		for _, recording := range hit.recordings {
			recordings[recording.ID] = true
			score := hit.scores[recording.ID]
			for _, r := range recording.Releases {
				if r.TrackCount > 0 && len(e.Tracks) != r.TrackCount || r.MediumCount > 0 && discs > 0 && r.MediumCount != discs {
					continue
				}
				row := sources.Release{ID: r.ID, Title: r.Title, Artists: r.Artists, Date: r.Date, Country: r.Country}
				if r.TrackCount > 0 {
					list.counts[r.ID] = curation.ReleaseCounts{Total: &r.TrackCount}
				}
				page = append(page, row)
				scores[r.ID] = max(scores[r.ID], score)
			}
			if len(recording.Releases) == 0 {
				for _, id := range recording.ReleaseIDs {
					page = append(page, sources.Release{ID: id})
					scores[id] = max(scores[id], score)
				}
			}
		}
		list.recordingIDs[hit.trackID] = recordings
		pages = append(pages, page)
		trackScores = append(trackScores, scores)
		list.items = append(list.items, page...)
	}
	common := intersectReleases(pages)
	list.items = uniqueReleases(list.items)
	switch {
	case len(hits) == 1 && sampled >= 2:
		list.codes = []string{"acoustid_sparse"}
	case len(common) == 0:
		list.codes = []string{"acoustid_spread"}
	default:
		list.items = common
	}
	// 每首曲目取该发行上的最高录音分数，再取所有有结果曲目中的最低分。
	scores := make(map[string]float64, len(list.items))
	for _, row := range list.items {
		score := trackScores[0][row.ID]
		for _, track := range trackScores[1:] {
			score = min(score, track[row.ID])
		}
		scores[row.ID] = score
	}
	return list, scores
}

func fingerprintSingle(ctx context.Context, e catalog.CurationEvidence, f sources.RecordingFinder, hit fingerprintHit) (MatchResult, error) {
	var all, byLength []sources.Recording
	for _, r := range hit.recordings {
		row := sources.Recording{ID: r.ID, Title: r.Title, Artists: r.Artists, LengthMS: r.LengthMS}
		all = append(all, row)
		if _, within := compareLength(e.Tracks[0].DurationSeconds, row.LengthMS); within {
			byLength = append(byLength, row)
		}
	}
	if len(byLength) == 0 {
		return scoreCandidates(DecideSingle(e, "acoustid", all), hit.scores), nil
	}
	if len(byLength) > 3 {
		result := newMatch("single", "acoustid")
		result.Candidates = recordingCandidates("acoustid", byLength)
		blocked := markBlocked(e, result.Mode, result.Candidates)
		result.Snapshot = MatchSnapshot{Version: 1, Method: "acoustid", CandidateSetComplete: len(byLength) <= 100}
		switch {
		case len(byLength) > 100:
			result = uncertain(result, "list_truncated")
		case blocked == len(byLength):
			result.Status, result.Conflicts = "no_usable_candidates", candidateConflicts(result.Candidates)
		default:
			result = uncertain(result, "too_many")
		}
		return scoreCandidates(result, hit.scores), nil
	}
	var official []sources.Recording
	scores := map[string]float64{}
	for _, row := range byLength {
		r, err := queryResult(f.RecordingByID(ctx, row.ID))
		if err != nil {
			return MatchResult{}, err
		}
		if r.ID != "" {
			if _, ok := scores[r.ID]; !ok {
				official = append(official, r)
			}
			scores[r.ID] = max(scores[r.ID], hit.scores[row.ID])
		}
	}
	if len(official) == 0 {
		result := newMatch("single", "acoustid")
		result.Candidates = recordingCandidates("acoustid", byLength)
		markBlocked(e, result.Mode, result.Candidates)
		return scoreCandidates(uncertain(result), hit.scores), nil
	}
	return scoreCandidates(DecideSingle(e, "acoustid", official), scores), nil
}

func scoreCandidates(result MatchResult, scores map[string]float64) MatchResult {
	for i, c := range result.Candidates {
		result.Candidates[i].Score = scores[c.ExternalID]
	}
	return result
}
