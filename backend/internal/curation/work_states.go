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
	ID, TargetType, TargetID, Kind, Status string
	RuleVersion                            int16
	InputDigest                            string
	Evidence, Candidates, Conflicts        json.RawMessage
	Resolution                             *string
	CreatedAt, UpdatedAt                   time.Time
	ResolvedAt                             *time.Time
}

func UpsertPendingItem(ctx context.Context, q Execer, item PendingItem) error {
	_, err := q.ExecContext(ctx, `INSERT INTO curation_pending_items(target_type,target_id,kind,status,rule_version,input_digest,evidence,candidates,conflicts)
 VALUES('release',$1,$2,'waiting_model',$3,$4,$5,COALESCE($6::jsonb,'[]'),COALESCE($7::jsonb,'[]'))
 ON CONFLICT(target_type,target_id,kind) WHERE status <> 'resolved' DO UPDATE SET rule_version=excluded.rule_version,input_digest=excluded.input_digest,
 evidence=excluded.evidence,candidates=excluded.candidates,conflicts=excluded.conflicts,updated_at=now()
 WHERE curation_pending_items.status='waiting_model'`, item.TargetID, item.Kind, item.RuleVersion, item.InputDigest, item.Evidence, item.Candidates, item.Conflicts)
	return err
}

func ResolvePendingItems(ctx context.Context, q Execer, targetID, kind, resolution string) error {
	_, err := q.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved', resolution=$3,updated_at=now(),resolved_at=now()
 WHERE target_type='release' AND target_id=$1 AND kind=$2 AND status IN ('waiting_model','shelved')`, targetID, kind, resolution)
	return err
}
