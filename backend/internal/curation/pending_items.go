package curation

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

const pendingColumns = `id,target_type,target_id,kind,status,rule_version,input_digest,evidence,candidates,conflicts,
 resolution,created_at,updated_at,resolved_at,coalesce(conversation_id::text,''),claimed_at,next_attempt_at,attempts,review_retries,shelved_reason`

func scanPending(row interface{ Scan(...any) error }) (PendingItem, error) {
	var p PendingItem
	err := row.Scan(&p.ID, &p.TargetType, &p.TargetID, &p.Kind, &p.Status, &p.RuleVersion, &p.InputDigest, &p.Evidence, &p.Candidates, &p.Conflicts, &p.Resolution, &p.CreatedAt, &p.UpdatedAt, &p.ResolvedAt, &p.ConversationID, &p.ClaimedAt, &p.NextAttemptAt, &p.Attempts, &p.ReviewRetries, &p.ShelvedReason)
	return p, err
}
func pendingRows(rows *sql.Rows, err error) ([]PendingItem, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PendingItem{}
	for rows.Next() {
		p, err := scanPending(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (w *Writer) PendingItem(ctx context.Context, id string) (PendingItem, error) {
	p, err := scanPending(w.db.QueryRowContext(ctx, `SELECT `+pendingColumns+` FROM curation_pending_items WHERE id=$1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, operations.NotFound("not_found", "待定项不存在")
	}
	return p, err
}

// 锁住预览对应的待定项，避免认领、交给人工或程序先匹配后仍按旧计划写入。
func CheckMatchItem(ctx context.Context, tx *sql.Tx, item PendingItem, conversation string, confirmed bool) error {
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_pending_items
 WHERE id=$1 AND target_id=$2 AND kind=$3 AND input_digest=$4
 AND ((NOT $6 AND conversation_id=NULLIF($5,'')::uuid AND status='in_progress') OR ($6 AND status='waiting_human')) FOR SHARE)`,
		item.ID, item.TargetID, item.Kind, item.InputDigest, conversation, confirmed).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return operations.Conflict("item_stale", "待定项已变更")
	}
	return nil
}

func (w *Writer) ClaimPendingItems(ctx context.Context, conversation string, now time.Time, n int) ([]PendingItem, error) {
	return pendingRows(w.db.QueryContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`), picked AS (
 SELECT p.id FROM curation_pending_items p JOIN visible v ON v.id=p.target_id WHERE p.status='waiting_model'
 AND (p.next_attempt_at IS NULL OR p.next_attempt_at <= $2) ORDER BY p.created_at,p.id LIMIT $3 FOR UPDATE OF p SKIP LOCKED
 ), claimed AS (UPDATE curation_pending_items SET status='in_progress',conversation_id=$1,claimed_at=$2,updated_at=now(),next_attempt_at=NULL
 WHERE id IN (SELECT id FROM picked) RETURNING *) SELECT `+pendingColumns+` FROM claimed ORDER BY created_at,id`, conversation, now, n))
}

func (w *Writer) ClaimedItems(ctx context.Context, conversation string) ([]PendingItem, error) {
	return pendingRows(w.db.QueryContext(ctx, `SELECT `+pendingColumns+` FROM curation_pending_items
 WHERE status='in_progress' AND conversation_id=$1 ORDER BY created_at,id`, conversation))
}

func (w *Writer) ReleaseClaimed(ctx context.Context, conversation string, countAttempt bool, nextAttemptAt *time.Time) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_pending_items SET
 attempts=attempts+$2::boolean::int,status=CASE WHEN $2 AND attempts>=2 THEN 'shelved' ELSE 'waiting_model' END,
 shelved_reason=CASE WHEN $2 AND attempts>=2 THEN 'unhandled' END,
 conversation_id=NULL,claimed_at=NULL,review_retries=0,next_attempt_at=$3,updated_at=now()
 WHERE status='in_progress' AND conversation_id=$1`, conversation, countAttempt, nextAttemptAt)
	return err
}
func (w *Writer) RetryClaimedReview(ctx context.Context, conversation string, next time.Time) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_pending_items SET review_retries=review_retries+1,next_attempt_at=$2,updated_at=now()
 WHERE conversation_id=$1 AND status='in_progress' AND review_retries<3`, conversation, next)
	return err
}

func (w *Writer) RecoverInProgress(ctx context.Context) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_pending_items SET status='waiting_model',conversation_id=NULL,claimed_at=NULL,review_retries=0,next_attempt_at=NULL,updated_at=now() WHERE status='in_progress'`)
	return err
}

func (w *Writer) ShelvePendingItem(ctx context.Context, id, conversation, reason string) error {
	if len(reason) == 0 || len(reason) > 1000 {
		return operations.Invalid("invalid_input", "搁置原因长度无效")
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if run, ok := ModelRunFrom(ctx); ok {
		actor, err := activeEnabler(ctx, tx)
		if err != nil {
			return err
		}
		if actor != run.ActorID || run.ConversationID != conversation || run.PendingItemID != id {
			return operations.Conflict("out_of_batch", "待定项不属于本次运行")
		}
	}
	result, err := tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='shelved',shelved_reason=$3,next_attempt_at=NULL,updated_at=now()
 WHERE id=$1 AND conversation_id=$2 AND status='in_progress'`, id, conversation, reason)
	if err := requireChanged(result, err, "out_of_batch", "待定项不属于本次运行"); err != nil {
		return err
	}
	return tx.Commit()
}

func ResolvePendingItem(ctx context.Context, q Execer, id, conversation, resolution string) error {
	result, err := q.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution=$3,resolved_at=coalesce(resolved_at,now()),updated_at=now(),next_attempt_at=NULL
 WHERE id=$1 AND ((status='in_progress' AND conversation_id=NULLIF($2,'')::uuid) OR (status='waiting_human' AND $2='') OR (status='resolved' AND resolution=$3))`, id, conversation, resolution)
	return requireChanged(result, err, "item_stale", "待定项状态已变更")
}

func requireChanged(result sql.Result, err error, code, message string) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return operations.Conflict(code, message)
	}
	return err
}
