package program

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

// 重查期间仍保留首次未收录的时间，来源退避后从同一模式接上。
type releaseReviewState struct {
	UnlistedSince *time.Time  `json:"unlisted_since,omitempty"`
	RecheckedAt   *time.Time  `json:"rechecked_at,omitempty"`
	Reason        string      `json:"reason,omitempty"`
	Retry         *matchRetry `json:"retry,omitempty"`
	BlockDigest   string      `json:"block_digest,omitempty"`
}

func previousReview(s curation.WorkState) (releaseReviewState, error) {
	var r releaseReviewState
	if len(s.Result) == 0 {
		return r, nil
	}
	err := json.Unmarshal(s.Result, &r)
	return r, err
}

func (w *Worker) searchRelease(ctx context.Context, u *unit, e catalog.CurationEvidence, fingerprintRequired bool, cursor *matchRetry) (MatchResult, error) {
	if err := w.sources.Check(ctx, "musicbrainz"); err != nil {
		return MatchResult{}, err
	}
	r, ok := w.sources.ReleaseFinder("musicbrainz")
	f, okRec := w.sources.RecordingFinder("musicbrainz")
	if !ok || !okRec {
		return MatchResult{}, operations.Conflict("source_disabled", "识别来源未启用")
	}
	a, _ := w.sources.ArtistFinder("musicbrainz")
	step, err := w.fingerprintStep(ctx)
	if err != nil {
		return MatchResult{}, err
	}
	if fingerprintRequired && step == nil {
		// 这次领取只补先前不可用的声纹；插件又关闭时保留原结论。
		return MatchResult{}, errQueueUnchanged
	}
	names := SearchNames{}
	if w.opts.CanonicalNames != nil {
		trackID := ""
		if e.CandidateKind == "loose_unknown" && len(e.Tracks) == 1 {
			trackID = e.Tracks[0].ID
		}
		names, err = w.opts.CanonicalNames(ctx, e.ID, trackID)
		if err != nil {
			return MatchResult{}, err
		}
	}
	if names.ArtistID == "" {
		names.ArtistID = e.ArtistMBID
	}
	review, err := previousReview(u.releaseState)
	if err != nil {
		return MatchResult{}, err
	}
	if previous := review.Retry; previous != nil && u.releaseState.MatchRuleVersion == MatchRuleVersion && u.releaseState.IdentityDigest == u.identityDigest {
		*cursor = *previous
		cursor.resume = previous.Step
	}
	return matchWithOptions(ctx, e, r, f, step, MatchOptions{Artists: a, Names: names, retry: cursor})
}

func (w *Worker) concludeMatch(ctx context.Context, u *unit, e catalog.CurationEvidence, m MatchResult) (out outcome, err error) {
	out.match = m
	if m.Status == "matched" {
		out.group, out.artistLinks, out.proposals, err = w.applyMatch(ctx, u, e, m)
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
	if out.match.Route == "select" && len(out.match.Candidates) > 0 && len(out.match.modelCandidates()) == 0 {
		// 已见候选都受阻，页仍不完整时交给后续查找，第一档没有可选项。
		out.match.Route = "lookup"
	}
	if u.programOnly && out.match.Status != "matched" {
		out.match.Status, out.match.Route = "unlisted", ""
	} else if out.match.Route == "lookup" || out.match.Route == "web" {
		model, web, err := w.sources.MatchFallbackReadiness(ctx)
		if err != nil {
			return out, err
		}
		configured := model && (out.match.Route != "web" || web)
		if configured {
			out.match.Status = "uncertain"
		} else {
			out.match.Status, out.match.Route = "unlisted", ""
			out.matchReason = "model_unconfigured"
			if model {
				out.matchReason = "web_unavailable"
			}
		}
	}
	out.status = out.match.Status
	return out, nil
}

type stepFailure struct {
	retry *matchRetry
	err   error
}

func (e *stepFailure) Error() string { return e.err.Error() }
func (e *stepFailure) Unwrap() error { return e.err }
