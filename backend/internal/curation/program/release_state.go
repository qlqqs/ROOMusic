package program

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/curation"
	"github.com/qlqq/roomusic/backend/internal/sources"
)

type releaseDigests struct {
	full, identity, gap, file string
}

func releaseInputs(e catalog.CurationEvidence) releaseDigests {
	return releaseDigests{ReleaseDigest(e), IdentityDigest(e), GapDigest(e), e.FileDigest}
}

func (d releaseDigests) record(s *curation.WorkState) {
	s.InputDigest, s.IdentityDigest, s.GapDigest = d.full, d.identity, d.gap
	s.FileDigest = d.file
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
	if s.FileDigest != d.file || s.InputDigest == "" {
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
	if upgrade && (s.RequeryMatchVersion != 0 || recheckOnUpgrade(s.Status) || matchedUpgrade(version, s.Status, e)) {
		var claimed bool
		if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_pending_items
 WHERE target_id=$1 AND kind=$2 AND status IN ('in_progress','waiting_human') FOR UPDATE)`, e.ID, matchItemKind(e)).Scan(&claimed); err != nil || claimed {
			return true, nil, err
		}
		s.RequeryMatchVersion = MatchRuleVersion
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
	return w.backfillFieldCredits(ctx, tx, e, r)
}

func (w *Worker) backfillFieldCredits(ctx context.Context, tx *sql.Tx, e catalog.CurationEvidence, r *sources.Release) (catalog.CurationEvidence, error) {
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
 AND match_rule_version IS NOT DISTINCT FROM NULLIF($10::smallint,0) AND patrol_revision=$11 RETURNING updated_at`,
		e.ID, LocalRuleVersion, version, d.full, d.identity, d.gap, s.RequeryMatchVersion, s.Generation, s.InputDigest, s.MatchRuleVersion, s.PatrolRevision).Scan(&s.UpdatedAt)
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
 AND (identity_digest=$4 OR (identity_digest IS NULL AND input_digest=$7 AND input_digest=$5))`,
		e.ID, matchItemKind(e), version, d.identity, d.full, jsonValue(pendingMatchEvidence(e, result.Method)), previous.InputDigest)
	return err
}

// 将来需要复核已匹配的规则在这里注明版本门槛与适用级别，105 没有此项升级。
var matchedRecheckVersions = map[string]int16{}

func matchedUpgrade(previous int16, status string, e catalog.CurationEvidence) bool {
	if status != "matched" {
		return false
	}
	level := "release"
	if e.ExternalID == "" {
		if len(e.Tracks) != 1 || e.CandidateKind != "loose_unknown" || e.Tracks[0].ExternalID == "" {
			return false
		}
		level = "recording"
	}
	threshold := matchedRecheckVersions[level]
	return previous < threshold && threshold <= MatchRuleVersion
}

func recheckOnUpgrade(status string) bool {
	switch status {
	case "unmatched", "conflict", "query_error", "uncertain", "no_match", "unlisted", "no_usable_candidates":
		return true
	}
	return false
}
