package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

type WorkState struct {
	Subject, TargetID                                       string
	RuleVersion                                             int16
	LocalRuleVersion, MatchRuleVersion, RequeryMatchVersion int16
	IdentityDigest, GapDigest                               string
	InputDigest, Status                                     string
	Attempts, Generation, RetryCount                        int
	NextAttemptAt, ManualRequestedAt                        *time.Time
	Result                                                  json.RawMessage
	UpdatedAt                                               time.Time
}

// ProgramChanges 是调度用的变化标记。发行轮的标记排除程序自己的写入，
// 避免取歌词期间每新增一条歌词记录就重新扫描发行。
type ProgramChanges struct {
	Scan, Settings, Sources, Suppression, Operation, OtherOperation sql.NullTime
	ArtistSettings, LatestDue, LatestReleaseDue                     sql.NullTime
	Suppressions, Due, ReleaseDue                                   int64
}

func LoadProgramChanges(ctx context.Context, q operations.Queryer, now time.Time) (ProgramChanges, error) {
	var c ProgramChanges
	err := q.QueryRowContext(ctx, `SELECT
 (SELECT max(finished_at) FROM scan_runs),
 (SELECT updated_at FROM curation_settings WHERE id=1),
 (SELECT u.updated_at FROM steward_user_settings u JOIN curation_settings c ON c.enabled_by=u.user_id WHERE c.id=1),
 GREATEST((SELECT max(updated_at) FROM curation_source_settings), (SELECT max(updated_at) FROM curation_source_keys)),
 (SELECT max(created_at) FROM curation_suppressions),
 (SELECT max(created_at) FROM operations),
 (SELECT o.created_at FROM operations o WHERE NOT EXISTS
   (SELECT 1 FROM curation_batches b WHERE b.id=o.batch_id AND b.tier='program')
   ORDER BY o.created_at DESC LIMIT 1),
 (SELECT count(*) FROM curation_suppressions),
 count(*),count(*) FILTER (WHERE subject IN ('release','artwork')),
 max(next_attempt_at),max(next_attempt_at) FILTER (WHERE subject IN ('release','artwork'))
 FROM curation_work_states WHERE status IN ('try_later','unlisted') AND next_attempt_at <= $1`, now).
		Scan(&c.Scan, &c.Settings, &c.ArtistSettings, &c.Sources, &c.Suppression, &c.Operation, &c.OtherOperation, &c.Suppressions, &c.Due, &c.ReleaseDue, &c.LatestDue, &c.LatestReleaseDue)
	return c, err
}

type Execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func LoadWorkStates(ctx context.Context, q operations.Queryer, subject string, targetIDs []string) (map[string]WorkState, error) {
	rows, err := q.QueryContext(ctx, `SELECT subject,target_id::text,rule_version,input_digest,status,attempts,generation,retry_count,next_attempt_at,result,updated_at,
 COALESCE(local_rule_version,0),COALESCE(match_rule_version,0),COALESCE(requery_match_version,0),COALESCE(identity_digest,''),COALESCE(gap_digest,''),manual_requested_at
 FROM curation_work_states WHERE subject=$1 AND target_id=ANY($2::uuid[])`, subject, targetIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make(map[string]WorkState)
	for rows.Next() {
		var s WorkState
		if err := rows.Scan(&s.Subject, &s.TargetID, &s.RuleVersion, &s.InputDigest, &s.Status, &s.Attempts, &s.Generation, &s.RetryCount, &s.NextAttemptAt, &s.Result, &s.UpdatedAt, &s.LocalRuleVersion, &s.MatchRuleVersion, &s.RequeryMatchVersion, &s.IdentityDigest, &s.GapDigest, &s.ManualRequestedAt); err != nil {
			return nil, err
		}
		states[s.TargetID] = s
	}
	return states, rows.Err()
}

func SaveWorkState(ctx context.Context, q Execer, s WorkState) error {
	_, err := q.ExecContext(ctx, `INSERT INTO curation_work_states(subject,target_id,rule_version,input_digest,status,attempts,generation,retry_count,next_attempt_at,result,local_rule_version,match_rule_version,requery_match_version,identity_digest,gap_digest)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,COALESCE($10::jsonb,'{}'),NULLIF($11::smallint,0),NULLIF($12::smallint,0),NULLIF($13::smallint,0),NULLIF($14,''),NULLIF($15,''))
 ON CONFLICT(subject,target_id) DO UPDATE SET rule_version=excluded.rule_version,input_digest=excluded.input_digest,status=excluded.status,
 attempts=excluded.attempts,generation=excluded.generation,retry_count=excluded.retry_count,result=excluded.result,updated_at=now(),
 local_rule_version=excluded.local_rule_version,match_rule_version=excluded.match_rule_version,requery_match_version=excluded.requery_match_version,
 identity_digest=excluded.identity_digest,gap_digest=excluded.gap_digest,
 needs_work=curation_work_states.manual_requested_at IS DISTINCT FROM $16::timestamptz,
 manual_requested_at=CASE WHEN curation_work_states.manual_requested_at IS DISTINCT FROM $16 THEN curation_work_states.manual_requested_at END,
 next_attempt_at=CASE WHEN curation_work_states.manual_requested_at IS DISTINCT FROM $16 AND excluded.status='unlisted'
 THEN curation_work_states.next_attempt_at ELSE excluded.next_attempt_at END`,
		s.Subject, s.TargetID, s.RuleVersion, s.InputDigest, s.Status, s.Attempts, s.Generation, s.RetryCount, s.NextAttemptAt, s.Result, s.LocalRuleVersion, s.MatchRuleVersion, s.RequeryMatchVersion, s.IdentityDigest, s.GapDigest, s.ManualRequestedAt)
	return err
}

type PendingItem struct {
	ConversationID                                          string
	ClaimedAt, NextAttemptAt                                *time.Time
	ItemToolCalls                                           int
	ItemTokens                                              int64
	Attempts, ReviewRetries                                 int
	ShelvedReason                                           *string
	ID, TargetType, TargetID, ArtistID, Kind, Status, Route string
	RuleVersion, MatchRuleVersion                           int16
	IdentityDigest                                          string
	InputDigest                                             string
	Evidence, Candidates, Conflicts                         json.RawMessage
	Resolution                                              *string
	CreatedAt, UpdatedAt                                    time.Time
	ResolvedAt                                              *time.Time
}

// 程序再见到同一组候选时，保留模型已升级的路线及待求证事实。
const sameModelInputsSQL = `(COALESCE(p.identity_digest,p.input_digest)=COALESCE(excluded.identity_digest,excluded.input_digest)
 AND p.candidates=excluded.candidates AND p.conflicts=excluded.conflicts)`

func UpsertPendingItem(ctx context.Context, q Execer, item PendingItem) error {
	target, insert := item.TargetID, `INSERT INTO curation_pending_items AS p(target_type,target_id,kind,status,rule_version,input_digest,evidence,candidates,conflicts,match_rule_version,identity_digest,route)
 SELECT 'release',$1,$2,CASE WHEN $10 IN ('lookup','web') THEN 'waiting_route' ELSE 'waiting_model' END,$3,$4,$5,COALESCE($6::jsonb,'[]'),COALESCE($7::jsonb,'[]'),NULLIF($8::smallint,0),NULLIF($9,''),NULLIF($10,'')
 WHERE NOT EXISTS (SELECT FROM curation_pending_items old WHERE old.target_type='release' AND old.target_id=$1 AND old.kind=$2
 AND old.resolution='suggestion_recorded' AND COALESCE(old.identity_digest,old.input_digest)=COALESCE(NULLIF($9,''),$4)
 AND old.candidates=COALESCE($6::jsonb,'[]') AND old.conflicts=COALESCE($7::jsonb,'[]'))
 ON CONFLICT(target_type,target_id,kind) WHERE status <> 'resolved' AND kind NOT IN ('verify_artist_credit','verify_artist_merge','verify_name')`
	if item.TargetType == "artist" {
		target, insert = item.ArtistID, `INSERT INTO curation_pending_items AS p(target_type,artist_id,kind,status,rule_version,input_digest,evidence,candidates,conflicts,match_rule_version,identity_digest,route)
 SELECT 'artist',$1,$2,CASE WHEN $10 IN ('lookup','web') THEN 'waiting_route' ELSE 'waiting_model' END,$3,$4,$5,COALESCE($6::jsonb,'[]'),COALESCE($7::jsonb,'[]'),NULLIF($8::smallint,0),NULLIF($9,''),NULLIF($10,'')
 WHERE NOT EXISTS (SELECT FROM curation_pending_items old WHERE old.target_type='artist' AND old.artist_id=$1 AND old.kind=$2
 AND old.resolution IN ('suggestion_recorded','no_result') AND old.input_digest=$4 AND old.candidates=COALESCE($6::jsonb,'[]')
 AND old.conflicts=COALESCE($7::jsonb,'[]'))
 ON CONFLICT(artist_id,kind) WHERE status <> 'resolved' AND target_type='artist'`
	}
	_, err := q.ExecContext(ctx, insert+` DO UPDATE SET rule_version=excluded.rule_version,input_digest=excluded.input_digest,
 match_rule_version=excluded.match_rule_version,identity_digest=excluded.identity_digest,
 evidence=CASE WHEN `+sameModelInputsSQL+` AND p.evidence ? 'unverified_facts'
 THEN excluded.evidence || jsonb_build_object('unverified_facts',p.evidence->'unverified_facts') ELSE excluded.evidence END,
 candidates=excluded.candidates,conflicts=excluded.conflicts,updated_at=now(),
 route=CASE WHEN `+sameModelInputsSQL+` AND p.route IN ('lookup','web') THEN p.route ELSE excluded.route END,
 status=CASE WHEN p.status='in_progress' THEN p.status WHEN `+sameModelInputsSQL+` AND p.route IN ('lookup','web')
 THEN 'waiting_route' ELSE excluded.status END,
 attempts=CASE WHEN p.status='shelved' THEN 0 ELSE p.attempts END,
 shelved_reason=CASE WHEN p.status='in_progress' THEN p.shelved_reason END,
 conversation_id=CASE WHEN p.status='in_progress' THEN p.conversation_id END,
 claimed_at=CASE WHEN p.status='in_progress' THEN p.claimed_at END,
 review_retries=CASE WHEN p.status='in_progress' THEN p.review_retries ELSE 0 END,
 next_attempt_at=CASE WHEN p.status IN ('waiting_model','in_progress') THEN p.next_attempt_at END
 WHERE p.status IN ('waiting_model','waiting_route','in_progress')
 OR (p.status='shelved' AND (p.candidates IS DISTINCT FROM excluded.candidates OR
     CASE WHEN p.identity_digest IS NOT NULL AND excluded.identity_digest IS NOT NULL THEN p.identity_digest<>excluded.identity_digest
          ELSE p.input_digest<>excluded.input_digest OR p.conflicts IS DISTINCT FROM excluded.conflicts END))`, target, item.Kind, item.RuleVersion, item.InputDigest, item.Evidence, item.Candidates, item.Conflicts, item.MatchRuleVersion, item.IdentityDigest, item.Route)
	return err
}

func ResolvePendingItems(ctx context.Context, q Execer, targetID, kind, resolution string) error {
	if kind == "artist_identity" {
		_, err := q.ExecContext(ctx, `WITH closed AS (
 UPDATE curation_pending_items SET status='resolved',resolution=$2,resolved_at=now(),updated_at=now(),next_attempt_at=NULL
 WHERE target_type='artist' AND artist_id=$1 AND status<>'resolved' RETURNING id
 ) UPDATE curation_model_exceptions SET status='superseded',decided_at=now(),decided_by=NULL
 WHERE pending_item_id IN (SELECT id FROM closed) AND status='open'`, targetID, resolution)
		return err
	}
	_, err := q.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved', resolution=$3,updated_at=now(),resolved_at=now()
 WHERE target_type='release' AND target_id=$1 AND kind=$2 AND status IN ('waiting_model','waiting_route','shelved','waiting_human')`, targetID, kind, resolution)
	return err
}
