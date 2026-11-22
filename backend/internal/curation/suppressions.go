package curation

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/governance"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type Suppression struct {
	ID          string          `json:"id"`
	TargetType  string          `json:"target_type"`
	TargetID    string          `json:"target_id"`
	Subject     string          `json:"subject"`
	ValueHash   string          `json:"value_hash"`
	Value       json.RawMessage `json:"value"`
	OperationID *string         `json:"operation_id"`
	CreatedBy   string          `json:"created_by"`
	CreatedAt   time.Time       `json:"created_at"`
}

func (w *Writer) SuppressReleaseValue(ctx context.Context, tx *sql.Tx, releaseID, field string, value catalog.CorrectionIntent, revertID, actorID string) error {
	raw, hash, err := governance.AutoValueKey(field, value)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO curation_suppressions(target_type,target_id,subject,value_hash,value,operation_id,created_by)
 VALUES('release',$1::uuid,$2,$3,$4::jsonb,NULLIF($5,'')::uuid,$6::uuid)
 ON CONFLICT(target_type,target_id,subject,value_hash) DO NOTHING`, releaseID, field, hash, string(raw), revertID, actorID)
	return err
}

func isSuppressed(ctx context.Context, tx *sql.Tx, releaseID, field, hash string) (bool, error) {
	var found bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_suppressions
 WHERE target_type='release' AND target_id=$1::uuid AND subject=$2 AND value_hash=$3)`, releaseID, field, hash).Scan(&found)
	return found, err
}

func (w *Writer) Suppressions(ctx context.Context, releaseID string, page, pageSize int) ([]Suppression, int64, error) {
	filter := ""
	var args []any
	if releaseID != "" {
		if !uuid.Valid(releaseID) {
			return nil, 0, operations.Invalid("invalid_id", "发行标识无效")
		}
		filter = " WHERE target_type='release' AND target_id=$1::uuid"
		args = append(args, releaseID)
	}
	var total int64
	if err := w.db.QueryRowContext(ctx, "SELECT count(*) FROM curation_suppressions"+filter, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	query := `SELECT id::text,target_type,target_id::text,subject,value_hash,value,operation_id::text,created_by::text,created_at
 FROM curation_suppressions` + filter + " ORDER BY created_at DESC,id DESC"
	if releaseID != "" {
		query += " LIMIT $2 OFFSET $3"
	} else {
		query += " LIMIT $1 OFFSET $2"
	}
	rows, err := w.db.QueryContext(ctx, query, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []Suppression{}
	for rows.Next() {
		var s Suppression
		if err := rows.Scan(&s.ID, &s.TargetType, &s.TargetID, &s.Subject, &s.ValueHash, &s.Value, &s.OperationID, &s.CreatedBy, &s.CreatedAt); err != nil {
			return nil, 0, err
		}
		items = append(items, s)
	}
	return items, total, rows.Err()
}

func (w *Writer) ClearSuppression(ctx context.Context, id string) error {
	if !uuid.Valid(id) {
		return operations.Invalid("invalid_id", "标记标识无效")
	}
	result, err := w.db.ExecContext(ctx, "DELETE FROM curation_suppressions WHERE id=$1::uuid", id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return operations.NotFound("not_found", "不再提出标记不存在")
	}
	return err
}
