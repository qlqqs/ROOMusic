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
func (w *Worker) prepareRelease(ctx context.Context, e catalog.CurationEvidence, d releaseDigests, s *curation.WorkState) (bool, error) {
	if s.TargetID == "" {
		return false, nil
	}
	if s.IdentityDigest == "" {
		if s.InputDigest != d.full {
			return false, nil
		}
	} else if s.IdentityDigest != d.identity {
		return false, nil
	}
	if s.IdentityDigest == d.identity && s.InputDigest == d.full && s.GapDigest == d.gap &&
		s.LocalRuleVersion == LocalRuleVersion && s.MatchRuleVersion == MatchRuleVersion {
		return false, nil
	}
	tx, err := w.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	version := s.MatchRuleVersion
	if version == 0 {
		version = s.RuleVersion
	}
	upgrade := version != MatchRuleVersion
	if upgrade && s.Status != "matched" {
		decision, err := reviewPendingVersion(ctx, tx, e, d, *s)
		if err != nil || decision == waitForClaim {
			return true, err
		}
		if decision == requery {
			s.RequeryMatchVersion = MatchRuleVersion
		} else {
			s.RequeryMatchVersion = 0
		}
	}
	if err := updateReleaseProjection(ctx, tx, e, s, d, MatchRuleVersion); err != nil {
		return false, err
	}
	return upgrade, tx.Commit()
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
 match_rule_version=$3,identity_digest=$4,input_digest=$5,evidence=$6
 WHERE target_id=$1 AND kind=$2 AND status IN ('waiting_model','shelved','in_progress')
 AND (identity_digest=$4 OR (identity_digest IS NULL AND input_digest=$7))`,
		e.ID, matchItemKind(e), version, d.identity, d.full, jsonValue(pendingMatchEvidence(e, result.Method)), previous.InputDigest)
	return err
}

type candidateReview int

const (
	requery candidateReview = iota
	keepConclusion
	waitForClaim
)

// 已保存的候选足以保留待定结论；确认新对应所需的完整结构不触发版本补查。
func reviewSavedCandidates(e catalog.CurationEvidence, candidates []Candidate, conflicts []string) candidateReview {
	if slices.Contains(conflicts, "insufficient_evidence") {
		return keepConclusion
	}
	if matchItemKind(e) == "recording_match" && len(e.Tracks) != 1 {
		return requery
	}
	for _, candidate := range candidates {
		if candidate.Source == "musicbrainz" && candidate.ExternalID != "" {
			return keepConclusion
		}
	}
	return requery
}

func reviewPendingVersion(ctx context.Context, tx *sql.Tx, e catalog.CurationEvidence, d releaseDigests, state curation.WorkState) (candidateReview, error) {
	var status, identity, full string
	var rawCandidates, rawConflicts []byte
	err := tx.QueryRowContext(ctx, `SELECT status,COALESCE(identity_digest,''),input_digest,candidates,conflicts FROM curation_pending_items
 WHERE target_id=$1 AND kind=$2 AND status<>'resolved' FOR UPDATE`, e.ID, matchItemKind(e)).
		Scan(&status, &identity, &full, &rawCandidates, &rawConflicts)
	if errors.Is(err, sql.ErrNoRows) {
		var result struct{ Conflicts []string }
		if err := json.Unmarshal(state.Result, &result); err != nil {
			return requery, err
		}
		return reviewSavedCandidates(e, nil, result.Conflicts), nil
	}
	if err != nil {
		return requery, err
	}
	if status == "in_progress" || status == "waiting_human" {
		return waitForClaim, nil
	}
	if identity != d.identity && !(identity == "" && full == d.full) {
		return requery, nil
	}
	var candidates []Candidate
	var conflicts []string
	if err := json.Unmarshal(rawCandidates, &candidates); err != nil {
		return requery, err
	}
	if err := json.Unmarshal(rawConflicts, &conflicts); err != nil {
		return requery, err
	}
	return reviewSavedCandidates(e, candidates, conflicts), nil
}
