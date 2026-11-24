package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

type WorkState struct {
	Subject, TargetID                string
	RuleVersion                      int16
	InputDigest, Status              string
	Attempts, Generation, RetryCount int
	NextAttemptAt                    *time.Time
	Result                           json.RawMessage
	UpdatedAt                        time.Time
}

// ProgramChanges 是调度用的变化标记。发行轮的标记排除程序自己的写入，
// 避免取歌词期间每新增一条歌词记录就重新扫描发行。
type ProgramChanges struct {
	Scan, Settings, Sources, Suppression, Operation, OtherOperation sql.NullTime
	LatestDue, LatestReleaseDue                                     sql.NullTime
	Suppressions, Due, ReleaseDue                                   int64
}

func LoadProgramChanges(ctx context.Context, q operations.Queryer, now time.Time) (ProgramChanges, error) {
	var c ProgramChanges
	err := q.QueryRowContext(ctx, `SELECT
 (SELECT max(finished_at) FROM scan_runs),
 (SELECT updated_at FROM curation_settings WHERE id=1),
 GREATEST((SELECT max(updated_at) FROM curation_source_settings), (SELECT max(updated_at) FROM curation_source_keys)),
 (SELECT max(created_at) FROM curation_suppressions),
 (SELECT max(created_at) FROM operations),
 (SELECT o.created_at FROM operations o WHERE NOT EXISTS
   (SELECT 1 FROM curation_batches b WHERE b.id=o.batch_id AND b.tier='program')
   ORDER BY o.created_at DESC LIMIT 1),
 (SELECT count(*) FROM curation_suppressions),
 count(*),count(*) FILTER (WHERE subject IN ('release','artwork')),
 max(next_attempt_at),max(next_attempt_at) FILTER (WHERE subject IN ('release','artwork'))
 FROM curation_work_states WHERE status='try_later' AND next_attempt_at <= $1`, now).
		Scan(&c.Scan, &c.Settings, &c.Sources, &c.Suppression, &c.Operation, &c.OtherOperation, &c.Suppressions, &c.Due, &c.ReleaseDue, &c.LatestDue, &c.LatestReleaseDue)
	return c, err
}

type Execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func LoadWorkStates(ctx context.Context, q operations.Queryer, subject string, targetIDs []string) (map[string]WorkState, error) {
	rows, err := q.QueryContext(ctx, `SELECT subject,target_id::text,rule_version,input_digest,status,attempts,generation,retry_count,next_attempt_at,result,updated_at
 FROM curation_work_states WHERE subject=$1 AND target_id=ANY($2::uuid[])`, subject, targetIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := make(map[string]WorkState)
	for rows.Next() {
		var s WorkState
		if err := rows.Scan(&s.Subject, &s.TargetID, &s.RuleVersion, &s.InputDigest, &s.Status, &s.Attempts, &s.Generation, &s.RetryCount, &s.NextAttemptAt, &s.Result, &s.UpdatedAt); err != nil {
			return nil, err
		}
		states[s.TargetID] = s
	}
	return states, rows.Err()
}

func SaveWorkState(ctx context.Context, q Execer, s WorkState) error {
	_, err := q.ExecContext(ctx, `INSERT INTO curation_work_states(subject,target_id,rule_version,input_digest,status,attempts,generation,retry_count,next_attempt_at,result)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,COALESCE($10::jsonb,'{}'))
 ON CONFLICT(subject,target_id) DO UPDATE SET rule_version=excluded.rule_version,input_digest=excluded.input_digest,status=excluded.status,
 attempts=excluded.attempts,generation=excluded.generation,retry_count=excluded.retry_count,next_attempt_at=excluded.next_attempt_at,result=excluded.result,updated_at=now()`,
		s.Subject, s.TargetID, s.RuleVersion, s.InputDigest, s.Status, s.Attempts, s.Generation, s.RetryCount, s.NextAttemptAt, s.Result)
	return err
}

type PendingItem struct {
	ConversationID                         string
	ClaimedAt, NextAttemptAt               *time.Time
	Attempts, ReviewRetries                int
	ShelvedReason                          *string
	ID, TargetType, TargetID, Kind, Status string
	RuleVersion                            int16
	InputDigest                            string
	Evidence, Candidates, Conflicts        json.RawMessage
	Resolution                             *string
	CreatedAt, UpdatedAt                   time.Time
	ResolvedAt                             *time.Time
}

func UpsertPendingItem(ctx context.Context, q Execer, item PendingItem) error {
	_, err := q.ExecContext(ctx, `INSERT INTO curation_pending_items AS p(target_type,target_id,kind,status,rule_version,input_digest,evidence,candidates,conflicts)
 VALUES('release',$1,$2,'waiting_model',$3,$4,$5,COALESCE($6::jsonb,'[]'),COALESCE($7::jsonb,'[]'))
 ON CONFLICT(target_type,target_id,kind) WHERE status <> 'resolved' DO UPDATE SET rule_version=excluded.rule_version,input_digest=excluded.input_digest,
 evidence=excluded.evidence,candidates=excluded.candidates,conflicts=excluded.conflicts,updated_at=now(),
 status=CASE WHEN p.status='shelved' THEN 'waiting_model' ELSE p.status END,
 attempts=CASE WHEN p.status='shelved' THEN 0 ELSE p.attempts END,
 shelved_reason=CASE WHEN p.status='in_progress' THEN p.shelved_reason END,
 conversation_id=CASE WHEN p.status='in_progress' THEN p.conversation_id END,
 claimed_at=CASE WHEN p.status='in_progress' THEN p.claimed_at END,
 review_retries=CASE WHEN p.status='in_progress' THEN p.review_retries ELSE 0 END,
 next_attempt_at=CASE WHEN p.status IN ('waiting_model','in_progress') THEN p.next_attempt_at END
 WHERE p.status IN ('waiting_model','in_progress')
 OR (p.status='shelved' AND (p.input_digest<>excluded.input_digest
     OR p.candidates IS DISTINCT FROM excluded.candidates
     OR p.conflicts IS DISTINCT FROM excluded.conflicts))`, item.TargetID, item.Kind, item.RuleVersion, item.InputDigest, item.Evidence, item.Candidates, item.Conflicts)
	return err
}

func ResolvePendingItems(ctx context.Context, q Execer, targetID, kind, resolution string) error {
	_, err := q.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved', resolution=$3,updated_at=now(),resolved_at=now()
 WHERE target_type='release' AND target_id=$1 AND kind=$2 AND status IN ('waiting_model','shelved','waiting_human')`, targetID, kind, resolution)
	return err
}
