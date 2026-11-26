package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
)

type releaseDigests struct {
	full, identity, gap string
}

func releaseInputs(e catalog.CurationEvidence) releaseDigests {
	return releaseDigests{ReleaseDigest(e), IdentityDigest(e), GapDigest(e)}
}

func (d releaseDigests) record(s *curation.WorkState) {
	s.InputDigest, s.IdentityDigest, s.GapDigest = d.full, d.identity, d.gap
	s.LocalRuleVersion = LocalRuleVersion
}

func matchItemKind(e catalog.CurationEvidence) string {
	if e.CandidateKind == "loose_unknown" {
		return "recording_match"
	}
	return "release_match"
}

// prepareRelease 在输入相同时维护本地状态。外部规则升级单独完成离线复核，普通查询留给后续轮次。
func (w *Worker) prepareRelease(ctx context.Context, e catalog.CurationEvidence, d releaseDigests, s *curation.WorkState) (bool, *MatchResult, error) {
	if s.TargetID == "" {
		return false, nil, nil
	}
	version := s.MatchRuleVersion
	if version == 0 {
		version = s.RuleVersion
	}
	upgrade := version != MatchRuleVersion
	if s.IdentityDigest == "" {
		if s.InputDigest != d.full {
			return false, nil, nil
		}
	} else if s.IdentityDigest != d.identity && !(upgrade && (s.IdentityDigest == curation.ReleaseIdentityDigest101(e) || s.Status == "matched" && s.IdentityDigest == curation.ReleaseIdentityDigest102(e))) {
		return false, nil, nil
	}
	if !upgrade && s.InputDigest == d.full && s.IdentityDigest == d.identity && s.GapDigest == d.gap && s.LocalRuleVersion == LocalRuleVersion {
		return false, nil, nil
	}
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return false, nil, err
	}
	defer tx.Rollback()
	if upgrade && s.Status == "matched" && w.hasCachedCreditInput(e) {
		e, err = w.backfillCachedCredits(ctx, tx, e)
		if err != nil {
			return false, nil, err
		}
		d = releaseInputs(e)
	}
	if upgrade && s.Status != "matched" {
		review, m, err := reviewPendingVersion(ctx, tx, e, d, *s)
		if err != nil || review == waitForClaim {
			return true, nil, err
		}
		if m != nil {
			// 只结束取证事务，后面的受治理写入与在线识别共用一个入口。
			return false, m, tx.Commit()
		}
		s.RequeryMatchVersion = 0
		if review == requery {
			s.RequeryMatchVersion = MatchRuleVersion
		}
	}
	if err := updateReleaseProjection(ctx, tx, e, s, d, MatchRuleVersion); err != nil {
		return false, nil, err
	}
	return upgrade, nil, tx.Commit()
}

func (w *Worker) hasCachedCreditInput(e catalog.CurationEvidence) bool {
	return w.opts.CachedReleaseCredits != nil && e.ExternalID != "" &&
		(e.Automatic["album_artist"] || slices.ContainsFunc(e.Tracks, func(t catalog.CurationTrack) bool { return t.ArtistAutomatic }))
}

func (w *Worker) backfillCachedCredits(ctx context.Context, tx *sql.Tx, e catalog.CurationEvidence) (catalog.CurationEvidence, error) {
	r, err := w.opts.CachedReleaseCredits(e.ExternalID)
	if err != nil || r == nil {
		return e, err
	}
	byRecording := releaseTrackCredits(r)
	byRecording[""] = &curation.ReleaseArtistCredit{ReleaseID: r.ID, Artists: r.Artists}
	if err := curation.BackfillFieldArtistLinks(ctx, tx, e.ID, r.ID, byRecording); err != nil {
		return e, err
	}
	// 新对应也可能属于已有的扫描署名，用事务内的最新艺人编号登记识别摘要。
	fresh, err := catalog.LoadCurationEvidence(ctx, tx, []string{e.ID}, w.writer.ArtworkExists)
	if err != nil {
		return e, err
	}
	return fresh[e.ID], nil
}

// 本地投影与模型待定项使用同一份目录输入。模型也可并发写入匹配结论。
func updateReleaseProjection(ctx context.Context, tx *sql.Tx, e catalog.CurationEvidence, s *curation.WorkState, d releaseDigests, version int16) error {
	if err := refreshMatchItem(ctx, tx, e, *s, d, version); err != nil {
		return err
	}
	err := tx.QueryRowContext(ctx, `UPDATE curation_work_states SET
 local_rule_version=$2,match_rule_version=$3,input_digest=$4,identity_digest=$5,gap_digest=$6,requery_match_version=NULLIF($7::smallint,0),
 updated_at=now() WHERE subject='release' AND target_id=$1 AND generation=$8 AND input_digest=$9
 AND match_rule_version IS NOT DISTINCT FROM NULLIF($10::smallint,0) RETURNING updated_at`,
		e.ID, LocalRuleVersion, version, d.full, d.identity, d.gap, s.RequeryMatchVersion, s.Generation, s.InputDigest, s.MatchRuleVersion).Scan(&s.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return errReleaseChanged
	}
	if err == nil {
		d.record(s)
		s.MatchRuleVersion = version
	}
	return err
}

// 刷新同一识别输入的模型证据与完整校验，保留原项状态、候选和会话。
func refreshMatchItem(ctx context.Context, tx *sql.Tx, e catalog.CurationEvidence, previous curation.WorkState, d releaseDigests, version int16) error {
	var result struct{ Method string }
	if len(previous.Result) > 0 {
		if err := json.Unmarshal(previous.Result, &result); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE curation_pending_items SET
 match_rule_version=$3,identity_digest=$4,input_digest=$5,evidence=evidence || $6::jsonb
 WHERE target_id=$1 AND kind=$2 AND status IN ('waiting_model','waiting_route','shelved','in_progress')
 AND (identity_digest IN ($4,NULLIF($8,'')) OR (identity_digest IS NULL AND input_digest=$7))`,
		e.ID, matchItemKind(e), version, d.identity, d.full, jsonValue(pendingMatchEvidence(e, result.Method)), previous.InputDigest, previous.IdentityDigest)
	return err
}

type candidateReview int

const (
	requery candidateReview = iota
	keepConclusion
	waitForClaim
)

func changedMatchConflict(code string) bool {
	switch code {
	case "track_count", "position_conflict", "artist_mismatch", "tag_text_mismatch":
		return true
	}
	return false
}

func reviewPendingVersion(ctx context.Context, tx *sql.Tx, e catalog.CurationEvidence, d releaseDigests, state curation.WorkState) (candidateReview, *MatchResult, error) {
	var status, identity, full string
	var rawCandidates, rawConflicts, rawEvidence []byte
	err := tx.QueryRowContext(ctx, `SELECT status,COALESCE(identity_digest,''),input_digest,candidates,conflicts,evidence FROM curation_pending_items
 WHERE target_id=$1 AND kind=$2 AND status<>'resolved' FOR UPDATE`, e.ID, matchItemKind(e)).Scan(&status, &identity, &full, &rawCandidates, &rawConflicts, &rawEvidence)
	if errors.Is(err, sql.ErrNoRows) {
		var result struct{ Conflicts []string }
		if err = json.Unmarshal(state.Result, &result); err != nil {
			return requery, nil, err
		}
		if slices.Contains(result.Conflicts, "insufficient_evidence") && e.Classification().Class == catalog.Insufficient {
			return keepConclusion, nil, nil
		}
		if state.Status == "unlisted" || state.Status == "uncertain" || slices.ContainsFunc(result.Conflicts, changedMatchConflict) {
			return requery, nil, nil
		}
		return keepConclusion, nil, nil
	}
	if err != nil {
		return requery, nil, err
	}
	if status == "in_progress" || status == "waiting_human" {
		return waitForClaim, nil, nil
	}
	if identity == "" && full != d.full || identity != "" && identity != d.identity && identity != state.IdentityDigest {
		return requery, nil, nil
	}
	var candidates []Candidate
	var conflicts []string
	var evidence struct {
		Snapshot MatchSnapshot `json:"match_snapshot"`
	}
	if err = json.Unmarshal(rawCandidates, &candidates); err != nil {
		return requery, nil, err
	}
	if err = json.Unmarshal(rawConflicts, &conflicts); err != nil {
		return requery, nil, err
	}
	if err = json.Unmarshal(rawEvidence, &evidence); err != nil {
		return requery, nil, err
	}
	m := replayMatch(e, candidates, conflicts, evidence.Snapshot)
	if m.Status == "matched" || m.Status == "no_usable_candidates" {
		return keepConclusion, &m, nil
	}
	// 旧待定可能同时含已撤销和仍可处理的候选。保留阻止依据，第一档只能选可用项。
	if m.Status == "uncertain" && status != "waiting_route" {
		usable := m.modelCandidates()
		if len(usable) != len(m.Candidates) || len(evidence.Snapshot.Blocked) > 0 {
			_, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET candidates=$3,conflicts=$4,
 evidence=jsonb_set(evidence,'{match_snapshot}',$5::jsonb),route=CASE WHEN $6 THEN 'lookup' ELSE 'select' END,
 status=CASE WHEN $6 AND status='waiting_model' THEN 'waiting_route' ELSE status END
 WHERE target_id=$1 AND kind=$2 AND status IN ('waiting_model','shelved')`,
				e.ID, matchItemKind(e), jsonValue(usable), jsonValue(m.Conflicts), jsonValue(m.savedSnapshot()), len(usable) == 0)
			if err != nil || len(usable) == 0 {
				return requery, nil, err
			}
		}
	}
	if state.Status == "unlisted" || state.Status == "uncertain" || slices.ContainsFunc(conflicts, changedMatchConflict) {
		return requery, nil, nil
	}
	// 其余历史结论保留现有路线。
	return keepConclusion, nil, nil
}
