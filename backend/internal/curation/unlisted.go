package curation

import (
	"context"
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// RecheckUnlisted 将可见的未收录发行登记为到期，继续由普通程序轮处理。
func (w *Writer) RecheckUnlisted(ctx context.Context, releaseID string) error {
	if !uuid.Valid(releaseID) {
		return operations.Invalid("invalid_input", "发行编号无效")
	}
	var visible, queued bool
	err := w.db.QueryRowContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`), queued AS (
 UPDATE curation_work_states SET needs_work=true,manual_requested_at=clock_timestamp(),next_attempt_at=LEAST(COALESCE(next_attempt_at,now()),now()),updated_at=now()
 WHERE subject='release' AND target_id=$1 AND status='unlisted' AND EXISTS (SELECT FROM visible WHERE id=$1)
 RETURNING target_id)
 SELECT EXISTS(SELECT FROM visible WHERE id=$1), EXISTS(SELECT FROM queued)`, releaseID).Scan(&visible, &queued)
	if err != nil {
		return err
	}
	if !visible {
		return operations.NotFound("not_found", "发行不存在")
	}
	if !queued {
		return operations.Conflict("not_unlisted", "只有未收录的发行能要求程序重查")
	}
	return nil
}

// FinishUnlisted 在同一事务校验模型领取的项、关闭它并登记一次程序重查。
func (w *Writer) FinishUnlisted(ctx context.Context, itemID, expectedIdentity, conversation, reason string) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var releaseID string
	err = tx.QueryRowContext(ctx, `SELECT target_id::text FROM curation_pending_items
 WHERE id=$1 AND kind IN ('release_match','recording_match') AND identity_digest=$2 AND status='in_progress' AND route IN ('lookup','web')
 AND conversation_id=NULLIF($3,'')::uuid FOR UPDATE`, itemID, expectedIdentity, conversation).Scan(&releaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return operations.Conflict("item_stale", "待定项已变更")
	}
	if err != nil {
		return err
	}
	entries, err := catalog.LoadCurationEvidence(ctx, tx, []string{releaseID}, w.ArtworkExists)
	if err != nil {
		return err
	}
	if e, ok := entries[releaseID]; !ok || ReleaseIdentityDigest(e) != expectedIdentity {
		return operations.Conflict("item_stale", "发行识别依据已变化")
	}
	r, err := tx.ExecContext(ctx, `UPDATE curation_work_states SET status='unlisted',next_attempt_at=now()+interval '90 days',
 retry_count=0,result=(result-'route') || jsonb_build_object('unlisted_since',now(),'reason',$3::text),updated_at=now()
 WHERE subject='release' AND target_id=$1 AND identity_digest=$2 AND status='uncertain'`, releaseID, expectedIdentity, reason)
	if err = requireChanged(r, err, "item_stale", "发行识别依据已变化"); err != nil {
		return err
	}
	if err = ResolvePendingItem(ctx, tx, itemID, conversation, "unlisted"); err != nil {
		return err
	}
	return tx.Commit()
}
