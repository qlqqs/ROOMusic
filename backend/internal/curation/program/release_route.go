package program

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

// 重查期间仍保留首次未收录的时间，来源退避后从同一模式接上。
type releaseReviewState struct {
	Judge         *curation.JudgeCursor `json:"judge,omitempty"`
	Retry         *matchRetry           `json:"retry,omitempty"`
	Manual        bool                  `json:"manual_recheck,omitempty"`
	UnlistedSince *time.Time            `json:"unlisted_since,omitempty"`
	RecheckedAt   *time.Time            `json:"rechecked_at,omitempty"`
	Reason        string                `json:"reason,omitempty"`
}

func previousReview(s curation.WorkState) (releaseReviewState, error) {
	var r releaseReviewState
	if len(s.Result) == 0 {
		return r, nil
	}
	err := json.Unmarshal(s.Result, &r)
	return r, err
}

func (w *Worker) searchRelease(ctx context.Context, u *unit, e catalog.CurationEvidence, fingerprintRequired bool) (MatchResult, error) {
	configured, err := w.localConfigured(ctx, "release")
	if err != nil {
		return MatchResult{}, err
	}
	if configured {
		var ready bool
		if err := w.db.QueryRowContext(ctx, `SELECT `+localDependencySQL+` FROM (SELECT $1::uuid target_id) r`, e.ID).Scan(&ready); err != nil {
			return MatchResult{}, err
		}
		if !ready {
			return MatchResult{}, errWaitingForLocal
		}
	}
	cursor := &u.matchCursor
	step, err := w.fingerprintStep(ctx)
	if err != nil {
		return MatchResult{}, err
	}
	review, err := previousReview(u.releaseState)
	if err != nil {
		return MatchResult{}, err
	}
	if c := review.Judge; c != nil && c.Files == e.FileDigest && c.Version == MatchRuleVersion && c.Generation == u.releaseState.Generation && u.releaseState.IdentityDigest == u.identityDigest {
		cursor.Step = "model_hints"
		u.programOnly = true
		u.judge = c
		return w.runJudge(ctx, e, c)
	}
	if previous := review.Retry; previous != nil && u.releaseState.MatchRuleVersion == MatchRuleVersion && previous.Files == e.FileDigest && u.releaseState.IdentityDigest == u.identityDigest {
		*cursor = *previous
		cursor.resume = previous.Step
	}
	fixed := (u.releaseState.Status == "matched" || cursor.resume == "existing_link") && (e.ExternalID != "" || e.CandidateKind == "loose_unknown" && len(e.Tracks) == 1 && e.Tracks[0].ExternalID != "")
	if allowed, _ := releaseAdmission(e, step != nil); !allowed && !fixed {
		m := newMatch("album", "")
		if e.CandidateKind == "loose_unknown" {
			m.Mode = "single"
		}
		m.Conflicts = []string{"insufficient_evidence"}
		return m, nil
	}
	if err := w.sources.Check(ctx, "musicbrainz"); err != nil {
		return MatchResult{}, err
	}
	r, ok := w.sources.ReleaseFinder("musicbrainz")
	f, okRec := w.sources.RecordingFinder("musicbrainz")
	if !ok || !okRec {
		return MatchResult{}, operations.Conflict("source_disabled", "识别来源未启用")
	}
	a, _ := w.sources.ArtistFinder("musicbrainz")
	if fixed {
		cursor.enter("existing_link")
		if e.ExternalID != "" {
			release, err := queryResult(r.ReleaseByID(ctx, e.ExternalID))
			if err != nil {
				return MatchResult{}, err
			}
			if release.ID != e.ExternalID {
				return uncertain(newMatch("album", "tag_id"), "existing_link"), nil
			}
			return DecideAlbum(e, "tag_id", []sources.Release{release}), nil
		}
		recording, err := queryResult(f.RecordingByID(ctx, e.Tracks[0].ExternalID))
		if err != nil {
			return MatchResult{}, err
		}
		if recording.ID != e.Tracks[0].ExternalID {
			return uncertain(newMatch("single", "tag_id"), "existing_link"), nil
		}
		return DecideSingle(e, "tag_id", []sources.Recording{recording}), nil
	}
	if fingerprintRequired && step == nil {
		return MatchResult{}, errQueueUnchanged
	}
	return matchWithOptions(ctx, e, r, f, step, MatchOptions{Artists: a, retry: cursor})
}

func (w *Worker) concludeMatch(ctx context.Context, u *unit, e catalog.CurationEvidence, m MatchResult) (out outcome, err error) {
	out.match = m
	if m.Status == "matched" {
		if m.Level == "release_group" && m.GroupFirstReleaseDate == "" && e.Year == 0 && !e.Corrected["year"] && !e.Automatic["year"] && !u.offline && u.judge == nil {
			m.GroupFirstReleaseDate, err = w.sources.GroupFirstReleaseDate(ctx, "musicbrainz", m.GroupID)
			if err != nil {
				return out, err
			}
			out.match = m
		}
		out.group, out.artistLinks, out.proposals, err = w.applyMatch(ctx, u, e, m, &out)
		if errors.Is(err, errArtistCreditUnconfirmed) {
			out.match.Status, out.match.Route = "uncertain", "select"
			out.match.Level, out.match.Rule = "", ""
			err = nil
		} else if err == nil {
			out.trackFields = "done"
		}
	}
	if err != nil {
		return out, err
	}
	switch out.match.Status {
	case "matched":
		out.status = "matched"
	case "no_usable_candidates":
		out.status = "unmatched"
		out.matchReason = "candidates_revoked"
		out.match.Route = ""
	case "uncertain":
		out.status = "conflict"
		if m.Method == "search" {
			if differentNamedGroups(curation.LocalSearchEvidence(e), out.match.Candidates) {
				out.match.Conflicts = []string{"different_release_groups"}
			} else {
				out.status = "unmatched"
			}
		} else if len(out.match.Conflicts) == 0 {
			out.match.Conflicts = []string{"insufficient_corroboration"}
		}
	default:
		out.status = "unmatched"
		if slices.Contains(out.match.Conflicts, "insufficient_evidence") {
			out.status = "done"
		}
	}
	if slices.Contains(out.match.Conflicts, "request_limit") {
		out.matchReason = "request_limit"
	}
	if slices.Contains(out.match.Conflicts, "ambiguous_editions") {
		out.matchReason = "ambiguous_editions"
	}
	if out.status == "conflict" || out.status == "unmatched" && out.matchReason == "" {
		model, err := curation.MatchModelConfigured(ctx, w.db)
		if err != nil {
			return out, err
		}
		if model && !u.programOnly {
			out.match.Route = "select"
		} else {
			out.match.Route = ""
		}
	}
	return out, nil
}

// 只有确知编号的两个发行组可以形成同名跨组冲突。
func differentNamedGroups(e catalog.CurationEvidence, candidates []Candidate) bool {
	var group string
	for _, c := range candidates {
		if c.Release == nil || c.Release.Group.ID == "" || blockedCodes(c.Conflicts) || !sameAlbumNames(e, *c.Release) {
			continue
		}
		if group != "" && group != c.Release.Group.ID {
			return true
		}
		group = c.Release.Group.ID
	}
	return false
}

type stepFailure struct {
	retry *matchRetry
	err   error
}

func (e *stepFailure) Error() string { return e.err.Error() }
func (e *stepFailure) Unwrap() error { return e.err }
