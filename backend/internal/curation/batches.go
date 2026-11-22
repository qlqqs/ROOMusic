package curation

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type Batch struct {
	ID         string     `json:"id"`
	Tier       string     `json:"tier"`
	OpenedAt   time.Time  `json:"opened_at"`
	ClosedAt   *time.Time `json:"closed_at"`
	Operations int64      `json:"operations"`
	Reverted   int64      `json:"reverted"`
}

func (w *Writer) OpenBatch(ctx context.Context, tier string) (Batch, error) {
	if tier != "program" && tier != "model" {
		return Batch{}, operations.Invalid("invalid_input", "批次层级无效")
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return Batch{}, err
	}
	defer tx.Rollback()
	actor, err := activeEnabler(ctx, tx)
	if err != nil {
		return Batch{}, err
	}
	var b Batch
	err = tx.QueryRowContext(ctx, `INSERT INTO curation_batches(tier,actor_id) VALUES($1,$2::uuid)
 RETURNING id::text,tier,opened_at`, tier, actor).Scan(&b.ID, &b.Tier, &b.OpenedAt)
	if err != nil {
		return Batch{}, err
	}
	return b, tx.Commit()
}

func (w *Writer) CloseBatch(ctx context.Context, id string) error {
	if !uuid.Valid(id) {
		return operations.Invalid("invalid_id", "批次标识无效")
	}
	result, err := w.db.ExecContext(ctx, `UPDATE curation_batches SET closed_at=COALESCE(closed_at,now()) WHERE id=$1::uuid`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return operations.NotFound("not_found", "整理批次不存在")
	}
	return err
}

func (w *Writer) CloseOpenBatches(ctx context.Context, tier string) error {
	if tier != "program" && tier != "model" {
		return operations.Invalid("invalid_input", "批次层级无效")
	}
	_, err := w.db.ExecContext(ctx, `UPDATE curation_batches SET closed_at=now() WHERE tier=$1 AND closed_at IS NULL`, tier)
	return err
}

func requireOpenBatch(ctx context.Context, tx *sql.Tx, id string) error {
	var open bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_batches WHERE id=$1::uuid AND closed_at IS NULL FOR SHARE)`, id).Scan(&open)
	if err == nil && !open {
		return operations.Conflict("batch_closed", "整理批次不存在或已经结束")
	}
	return err
}

const batchProjection = `SELECT b.id::text,b.tier,b.opened_at,b.closed_at,counts.operations,counts.reverted
 FROM curation_batches b CROSS JOIN LATERAL (
 SELECT count(*) AS operations,count(r.id) AS reverted
 FROM operations o LEFT JOIN operations r ON r.reverts_operation_id=o.id WHERE o.batch_id=b.id
 ) counts`

func scanBatch(row interface{ Scan(...any) error }) (Batch, error) {
	var b Batch
	err := row.Scan(&b.ID, &b.Tier, &b.OpenedAt, &b.ClosedAt, &b.Operations, &b.Reverted)
	return b, err
}

func (w *Writer) Batch(ctx context.Context, id string) (Batch, error) {
	if !uuid.Valid(id) {
		return Batch{}, operations.Invalid("invalid_id", "批次标识无效")
	}
	b, err := scanBatch(w.db.QueryRowContext(ctx, batchProjection+" WHERE b.id=$1::uuid", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Batch{}, operations.NotFound("not_found", "整理批次不存在")
	}
	return b, err
}

func (w *Writer) Batches(ctx context.Context, page, pageSize int) ([]Batch, int64, error) {
	var total int64
	if err := w.db.QueryRowContext(ctx, "SELECT count(*) FROM curation_batches").Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := w.db.QueryContext(ctx, batchProjection+" ORDER BY b.opened_at DESC,b.id DESC LIMIT $1 OFFSET $2", pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Batch{}
	for rows.Next() {
		b, err := scanBatch(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, b)
	}
	return items, total, rows.Err()
}
