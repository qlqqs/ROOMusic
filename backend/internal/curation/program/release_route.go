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
	UnlistedSince *time.Time `json:"unlisted_since,omitempty"`
	RecheckedAt   *time.Time `json:"rechecked_at,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	BlockDigest   string     `json:"block_digest,omitempty"`
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
	if err := w.sources.Check(ctx, "musicbrainz"); err != nil {
		return MatchResult{}, err
	}
	r, ok := w.sources.ReleaseFinder("musicbrainz")
	f, okRec := w.sources.RecordingFinder("musicbrainz")
	if !ok || !okRec {
		return MatchResult{}, operations.Conflict("source_disabled", "识别来源未启用")
	}
	a, _ := w.sources.ArtistFinder("musicbrainz")
	var step *FingerprintStep
	if !u.programOnly {
		step, err = w.fingerprintStep(ctx)
		if err != nil {
			return MatchResult{}, err
		}
		if fingerprintRequired && step == nil {
			return MatchResult{}, operations.Conflict("source_disabled", "识别来源未启用")
		}
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
	return matchWithOptions(ctx, e, r, f, step, MatchOptions{Artists: a, Names: names, ProgramOnly: u.programOnly})
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
