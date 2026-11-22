package operations

import (
	"context"
	"time"
)

// RevertCandidate 只含单条撤销的分派与续页所需的信息。
type RevertCandidate struct {
	ID        string
	Type      string
	CreatedAt time.Time
}

// CurationRevertCandidates 按批次或来源倒序取一段尚未撤销的操作。
func CurationRevertCandidates(ctx context.Context, db Queryer, batchID, provider string, before time.Time, beforeID string, limit int) ([]RevertCandidate, error) {
	scope := "o.batch_id=$1::uuid"
	value := batchID
	if provider != "" {
		scope = "o.origin->>'source' = 'curation' AND (o.origin->>'provider') = $1"
		value = provider
	}
	args := []any{value, limit}
	query := `SELECT o.id::text,o.operation_type,o.created_at
 FROM operations o
 LEFT JOIN (
   SELECT reverts_operation_id AS id FROM operations WHERE reverts_operation_id IS NOT NULL
 ) r ON r.id=o.id
 WHERE ` + scope + ` AND o.operation_type<>'revert' AND r.id IS NULL`
	if beforeID != "" {
		query += " AND (o.created_at,o.id)<($3::timestamptz,$4::uuid)"
		args = append(args, before, beforeID)
	}
	rows, err := db.QueryContext(ctx, query+" ORDER BY o.created_at DESC,o.id DESC LIMIT $2", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []RevertCandidate{}
	for rows.Next() {
		var item RevertCandidate
		if err := rows.Scan(&item.ID, &item.Type, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
