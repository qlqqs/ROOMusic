package curation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	return suppressField(ctx, tx, "release", releaseID, field, value, revertID, actorID)
}
func (w *Writer) SuppressTrackValue(ctx context.Context, tx *sql.Tx, trackID, field string, value catalog.CorrectionIntent, revertID, actorID string) error {
	return suppressField(ctx, tx, "track", trackID, field, value, revertID, actorID)
}
func suppressField(ctx context.Context, tx *sql.Tx, target, id, field string, value catalog.CorrectionIntent, revertID, actorID string) error {
	raw, hash, err := governance.AutoValueKey(field, value)
	if err != nil {
		return err
	}
	return insertSuppression(ctx, tx, target, id, field, hash, raw, revertID, actorID)
}

func (w *Writer) Suppressions(ctx context.Context, releaseID string, page, pageSize int) ([]Suppression, int64, error) {
	filter := ""
	var args []any
	if releaseID != "" {
		if !uuid.Valid(releaseID) {
			return nil, 0, operations.Invalid("invalid_id", "发行标识无效")
		}
		filter = ` WHERE (target_type='release' AND target_id=$1::uuid)
 OR (target_type='release_group' AND target_id=(SELECT group_id FROM releases WHERE id=$1::uuid))
 OR (target_type='track' AND target_id IN (SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id WHERE m.release_id=$1::uuid))`
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
	// 按 ID 顺序锁定标记，避免同时清除一对标记的两端时互相等待。
	result, err := w.db.ExecContext(ctx, `WITH requested AS (
		SELECT id, target_type, target_id, operation_id, CASE subject
			WHEN 'artwork' THEN 'artwork_source'
			WHEN 'artwork_source' THEN 'artwork'
			WHEN 'lyrics' THEN 'lyrics_source'
			WHEN 'lyrics_source' THEN 'lyrics'
		END AS paired_subject
		FROM curation_suppressions WHERE id=$1::uuid
	), matching AS (
		SELECT id FROM requested
		UNION ALL
		SELECT paired.id FROM requested
		JOIN curation_suppressions paired ON paired.target_type=requested.target_type
			AND paired.target_id=requested.target_id AND paired.subject=requested.paired_subject
			AND paired.operation_id=requested.operation_id
	)
	DELETE FROM curation_suppressions WHERE id IN (
		SELECT marker.id FROM curation_suppressions marker JOIN matching ON matching.id=marker.id
		ORDER BY marker.id FOR UPDATE OF marker
	)`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return operations.NotFound("not_found", "不再提出标记不存在")
	}
	return err
}

func hashExternalValue(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

type externalIdentity struct {
	Source     string `json:"source"`
	ExternalID string `json:"external_id"`
}

type externalSuppression struct {
	targetType, targetID, subject, contentHash string
	externalIdentity
}

func ExternalIdentitySuppressed(ctx context.Context, q operations.Queryer, targetType, targetID, subject, source, externalID string) (bool, error) {
	identity := externalIdentity{source, externalID}
	return suppressionExists(ctx, q, targetType, targetID, subject+"_source", identity.identityHash())
}

func LinkSuppressed(ctx context.Context, q operations.Queryer, objectType, objectID, source, externalID string) (bool, error) {
	identity := externalIdentity{source, externalID}
	return suppressionExists(ctx, q, objectType, objectID, "external_link", identity.identityHash())
}

func suppressionExists(ctx context.Context, q operations.Queryer, targetType, targetID, subject, hash string) (bool, error) {
	var found bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_suppressions WHERE target_type=$1 AND target_id=$2 AND subject=$3 AND value_hash=$4)`, targetType, targetID, subject, hash).Scan(&found)
	return found, err
}

func insertSuppression(ctx context.Context, tx *sql.Tx, targetType, targetID, subject, hash string, value any, revertID, actorID string) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO curation_suppressions(target_type,target_id,subject,value_hash,value,operation_id,created_by)
 VALUES($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,$7) ON CONFLICT(target_type,target_id,subject,value_hash) DO NOTHING`, targetType, targetID, subject, hash, raw, revertID, actorID)
	return err
}

func (s externalIdentity) identityHash() string {
	return hashExternalValue(s.Source + "\x00" + s.ExternalID)
}

func (s externalSuppression) exists(ctx context.Context, tx *sql.Tx) (bool, error) {
	var found bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM curation_suppressions
 WHERE target_type=$1 AND target_id=$2::uuid AND ((subject=$3 AND value_hash=$4) OR (subject=$5 AND value_hash=$6)))`,
		s.targetType, s.targetID, s.subject, s.contentHash, s.subject+"_source", s.identityHash()).Scan(&found)
	return found, err
}

func (s externalSuppression) insert(ctx context.Context, tx *sql.Tx, value any, revertID string) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO curation_suppressions(target_type,target_id,subject,value_hash,value,operation_id,created_by)
 VALUES($1,$2::uuid,$3,$4,$5::jsonb,$6::uuid,$7::uuid),($1,$2::uuid,$8,$9,$5::jsonb,$6::uuid,$7::uuid)
 ON CONFLICT(target_type,target_id,subject,value_hash) DO NOTHING`, s.targetType, s.targetID, s.subject, s.contentHash, string(raw), revertID, operations.ActorID(ctx), s.subject+"_source", s.identityHash())
	return err
}

func SuppressExternalLink(ctx context.Context, tx *sql.Tx, objectType, objectID, source, externalID, revertID, actorID string) error {
	identity := externalIdentity{source, externalID}
	return insertSuppression(ctx, tx, objectType, objectID, "external_link", identity.identityHash(), identity, revertID, actorID)
}
