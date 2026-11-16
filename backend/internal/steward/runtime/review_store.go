package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

func (s *Store) SaveReview(ctx context.Context, userID, conversationID string, review Review) (err error) {
	defer func() { err = storeError("保存管家审查", err) }()
	items, err := json.Marshal(review.Items)
	if err != nil {
		return err
	}
	usage, err := json.Marshal(review.Usage)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = lockConversation(ctx, tx, userID, conversationID); err != nil {
		return err
	}
	err = requireAffected(tx.ExecContext(ctx, `INSERT INTO steward_reviews(id,plan_id,plan_digest,evidence_digest,policy_version,model,status,items,usage,expires_at) SELECT $1::uuid,p.id,$3,$4,$5,$6,$7,$8::jsonb,$9::jsonb,$10 FROM steward_plans p WHERE p.id=$2::uuid AND p.user_id=$11::uuid AND p.conversation_id=$12::uuid AND p.status='pending'`, review.ID, review.PlanID, review.PlanDigest, review.EvidenceDigest, review.PolicyVersion, review.Model, review.Status, string(items), string(usage), review.ExpiresAt, userID, conversationID))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func readLatestReview(ctx context.Context, tx *sql.Tx, planID string) (review *Review, err error) {
	defer func() { err = storeError("读取管家审查", err) }()
	var r Review
	var items, usage []byte
	err = tx.QueryRowContext(ctx, `SELECT id::text,plan_id::text,plan_digest,evidence_digest,policy_version,model,status,items,usage,expires_at FROM steward_reviews WHERE plan_id=$1::uuid ORDER BY created_at DESC,id DESC LIMIT 1`, planID).Scan(&r.ID, &r.PlanID, &r.PlanDigest, &r.EvidenceDigest, &r.PolicyVersion, &r.Model, &r.Status, &items, &usage, &r.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(items, &r.Items); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(usage, &r.Usage); err != nil {
		return nil, err
	}
	r.ExpiresAt = r.ExpiresAt.UTC()
	return &r, nil
}
