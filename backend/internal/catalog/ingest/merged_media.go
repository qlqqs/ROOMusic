package ingest

import (
	"context"
	"database/sql"
	"errors"

	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// NextMergedMediaPosition 用于预览和写入；来源碟号较大时保留，否则接在当前末碟之后。
func NextMergedMediaPosition(lastPosition, originPosition int) int {
	return max(lastPosition+1, originPosition)
}

// vacateMergedMedia 先把并入碟移到当前最小碟号之下，给自有碟和新的正式碟号让位。
func vacateMergedMedia(ctx context.Context, tx *sql.Tx, targetID string) error {
	_, err := tx.ExecContext(ctx, `WITH temporary AS (
		SELECT id, (SELECT LEAST(MIN(position),0) FROM media WHERE release_id=$1::uuid)
			- ROW_NUMBER() OVER (ORDER BY id) AS position
		FROM media WHERE release_id=$1::uuid AND origin_release_id IS NOT NULL
	) UPDATE media m SET position=t.position FROM temporary t WHERE m.id=t.id`, targetID)
	return err
}

// LayoutMergedMedia 在持有目录写锁的事务里，把同一来源的碟依原碟号排在一起，先并入的来源在前。
func LayoutMergedMedia(ctx context.Context, tx *sql.Tx, targetID string) error {
	if err := vacateMergedMedia(ctx, tx, targetID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT m.id::text, m.origin_position,
		(SELECT COALESCE(MAX(position),0) FROM media WHERE release_id=$1::uuid AND origin_release_id IS NULL)
		FROM media m JOIN release_merges r ON r.source_release_id=m.origin_release_id
		WHERE m.release_id=$1::uuid ORDER BY r.merged_at,r.source_release_id,m.origin_position,m.id`, targetID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []string
	var positions []int
	var last int
	for rows.Next() {
		var id string
		var origin, ownLast int
		if err := rows.Scan(&id, &origin, &ownLast); err != nil {
			return err
		}
		last = NextMergedMediaPosition(max(last, ownLast), origin)
		ids = append(ids, id)
		positions = append(positions, last)
	}
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE media m SET position=p.position
		FROM unnest($1::uuid[], $2::integer[]) AS p(id,position)
		WHERE m.id=p.id AND m.position IS DISTINCT FROM p.position`, ids, positions)
	return err
}

func persistMedium(ctx context.Context, tx *sql.Tx, releaseID, mergeTargetID string, disc int, title string) (string, error) {
	if title == "" {
		title = "Medium"
	}
	var id string
	var occupied bool
	var err error
	if mergeTargetID != "" {
		err = tx.QueryRowContext(ctx, `SELECT id::text FROM media WHERE origin_release_id=$1::uuid AND origin_position=$2`, releaseID, disc).Scan(&id)
	} else {
		err = tx.QueryRowContext(ctx, `SELECT id::text,origin_release_id IS NOT NULL FROM media WHERE release_id=$1::uuid AND position=$2`, releaseID, disc).Scan(&id, &occupied)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if occupied {
		if err := vacateMergedMedia(ctx, tx, releaseID); err != nil {
			return "", err
		}
	} else if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE media SET title=$1 WHERE id=$2::uuid AND title IS DISTINCT FROM $1`, title, id)
		return id, err
	}
	id = uuid.New()
	if mergeTargetID != "" {
		_, err = tx.ExecContext(ctx, `INSERT INTO media(id,release_id,position,title,origin_release_id,origin_position)
			SELECT $1::uuid,$2::uuid,LEAST(MIN(position),0)-1,$3,$4::uuid,$5 FROM media WHERE release_id=$2::uuid`,
			id, mergeTargetID, title, releaseID, disc)
	} else {
		_, err = tx.ExecContext(ctx, `INSERT INTO media(id,release_id,position,title) VALUES($1::uuid,$2::uuid,$3,$4)`, id, releaseID, disc, title)
	}
	return id, err
}
