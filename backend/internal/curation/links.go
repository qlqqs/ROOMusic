package curation

import (
	"context"
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

type Link struct{ Source, ObjectType, ObjectID, ExternalID, Method, Confidence, OperationID string }

const linkColumns = `source,object_type,object_id::text,external_id,method,confidence,COALESCE(operation_id::text,'')`

func scanLink(row interface{ Scan(...any) error }) (Link, error) {
	var l Link
	err := row.Scan(&l.Source, &l.ObjectType, &l.ObjectID, &l.ExternalID, &l.Method, &l.Confidence, &l.OperationID)
	return l, err
}

func previousLink(row *sql.Row) (*Link, error) {
	l, err := scanLink(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

const insertLinkSQL = `INSERT INTO external_links(id,source,object_type,object_id,external_id,method,confidence,operation_id)
 VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,'')::uuid)`

func (l Link) insertArgs() []any {
	return []any{uuid.New(), l.Source, l.ObjectType, l.ObjectID, l.ExternalID, l.Method, l.Confidence, l.OperationID}
}

// PutLink 由 PostgreSQL 18 在同一次冲突更新中返回旧行，并发替换时也能得到实际被替换的值。
func PutLink(ctx context.Context, tx *sql.Tx, l Link) (*Link, error) {
	return previousLink(tx.QueryRowContext(ctx, `WITH changed AS (
 `+insertLinkSQL+`
 ON CONFLICT(source,object_type,object_id) DO UPDATE SET external_id=excluded.external_id,method=excluded.method,
 confidence=excluded.confidence,operation_id=excluded.operation_id,created_at=now()
 RETURNING OLD.*
 ) SELECT `+linkColumns+` FROM changed WHERE id IS NOT NULL`, l.insertArgs()...))
}

func RemoveLink(ctx context.Context, tx *sql.Tx, source, objectType, objectID string) (*Link, error) {
	return previousLink(tx.QueryRowContext(ctx, `DELETE FROM external_links WHERE source=$1 AND object_type=$2 AND object_id=$3 RETURNING `+linkColumns, source, objectType, objectID))
}

func LinksOf(ctx context.Context, q operations.Queryer, objectType string, objectIDs []string) ([]Link, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+linkColumns+` FROM external_links WHERE object_type=$1 AND object_id=ANY($2::uuid[]) ORDER BY object_id,source`, objectType, objectIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	links := []Link{}
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

func ObjectsLinkedTo(ctx context.Context, q operations.Queryer, source, objectType, externalID string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT object_id::text FROM external_links WHERE source=$1 AND object_type=$2 AND external_id=$3 ORDER BY object_id`, source, objectType, externalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
