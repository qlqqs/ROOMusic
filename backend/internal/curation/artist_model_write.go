package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/operations"
)

// 同一个工具输入可能分别写图片与简介；每步对账都绑定会话、待定项与完整输入。
func (run *ModelRun) ArtistToolFingerprint(kind string) string {
	return operations.Fingerprint([]string{kind, run.ConversationID, run.PendingItemID, run.ToolInputDigest})
}

func (w *Writer) ReplayArtistTool(ctx context.Context, key, kind string) (json.RawMessage, error) {
	run, ok := ModelRunFrom(ctx)
	if !ok || run.ToolInputDigest == "" {
		return nil, operations.Invalid("invalid_input", "艺人工具缺少调用依据")
	}
	var fingerprint string
	var response json.RawMessage
	err := w.db.QueryRowContext(ctx, `SELECT fingerprint,response FROM operations WHERE actor_id=$1 AND idempotency_key=$2`, run.ActorID, key).Scan(&fingerprint, &response)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.Join(operations.ErrUnavailable, err)
	}
	if fingerprint != run.ArtistToolFingerprint(kind) {
		return nil, operations.Conflict("idempotency_conflict", "该幂等键已用于不同的输入")
	}
	return response, nil
}

// 调用方确认本次提交已有资料写入后，结束当前待定项。
func (w *Writer) FinishArtistMedia(ctx context.Context, item PendingItem) error {
	run, ok := ModelRunFrom(ctx)
	if !ok || run.PendingItemID != item.ID {
		return operations.Conflict("out_of_batch", "待定项不属于本次运行")
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, _, err := lockEffectiveArtist(ctx, tx, item.ArtistID); err != nil {
		return err
	}
	if err := ResolvePendingItem(ctx, tx, item.ID, run.ConversationID, "model_media"); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errors.Join(operations.ErrUnavailable, err)
	}
	return nil
}
