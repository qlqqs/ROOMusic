package ingest

import (
	"context"
	"database/sql"
)

func (f *ScanFeedback) recordChanges(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `CREATE TEMP TABLE scan_feedback_changes ON COMMIT DROP AS
  SELECT COALESCE(a.kind,b.kind) kind,COALESCE(a.id,b.id) id,b.id IS NOT NULL existed_before,
   COALESCE(b.present,false) present_before,COALESCE(a.present,false) present_after,
   (CASE WHEN b.id IS NULL OR a.metadata IS DISTINCT FROM b.metadata THEN $1 ELSE 0 END |
    CASE WHEN a.location IS DISTINCT FROM b.location THEN $2 ELSE 0 END |
    CASE WHEN a.release_id IS DISTINCT FROM b.release_id THEN $3 ELSE 0 END |
    CASE WHEN a.present IS DISTINCT FROM b.present THEN $4 ELSE 0 END |
    CASE WHEN a.artwork IS DISTINCT FROM b.artwork THEN $5 ELSE 0 END |
    CASE WHEN NOT COALESCE(b.present,true) AND a.present THEN $6 ELSE 0 END)::integer change_mask
  FROM scan_feedback_before b FULL JOIN scan_feedback_after a ON a.kind=b.kind AND a.id=b.id`,
		ChangeMetadata, ChangeLocation, ChangeMembership, ChangeAvailability, ChangeArtwork, ChangeRestored)
	if err != nil {
		return err
	}
	// 曲目改挂同时归集旧发行与新发行，再传播到锁后读取的旧组与新组。
	for _, statement := range []string{
		`UPDATE scan_feedback_changes c SET change_mask=c.change_mask | causes.mask FROM (
   SELECT p.release_id,bit_or(t.change_mask) mask FROM scan_feedback_changes t
   JOIN (SELECT kind,id,release_id FROM scan_feedback_before UNION SELECT kind,id,release_id FROM scan_feedback_after) p
   ON p.kind=t.kind AND p.id=t.id WHERE t.kind='track' AND t.change_mask<>0 GROUP BY p.release_id
  ) causes WHERE c.kind='release' AND c.id=causes.release_id`,
		`UPDATE scan_feedback_changes c SET change_mask=c.change_mask | causes.mask FROM (
   SELECT p.group_id,bit_or(r.change_mask) mask FROM scan_feedback_changes r
   JOIN (SELECT kind,id,group_id FROM scan_feedback_before UNION SELECT kind,id,group_id FROM scan_feedback_after) p
   ON p.kind=r.kind AND p.id=r.id WHERE r.kind='release' AND r.change_mask<>0 GROUP BY p.group_id
  ) causes WHERE c.kind='album' AND c.id=causes.group_id`,
		`DELETE FROM scan_feedback_changes WHERE change_mask=0`,
		`ALTER TABLE scan_feedback_changes ADD PRIMARY KEY(kind,id)`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	// 两次扫描提交之间可能发生治理归组。只有本次操作实际改变可见性时才覆盖后态，
	// 否则封面/资料更新会把治理导致的空组误记为扫描消失。
	for {
		result, err := tx.ExecContext(ctx, `WITH batch AS (
   DELETE FROM scan_feedback_changes WHERE (kind,id) IN (SELECT kind,id FROM scan_feedback_changes ORDER BY kind,id LIMIT 256)
   RETURNING kind,id,existed_before,present_before,present_after,change_mask
  ) INSERT INTO scan_catalog_changes(scan_run_id,entity_kind,entity_id,existed_before,present_before,present_after,change_mask)
   SELECT $1::uuid,kind,id,existed_before,present_before,present_after,change_mask FROM batch
   ON CONFLICT(scan_run_id,entity_kind,entity_id) DO UPDATE SET present_after=CASE
    WHEN EXCLUDED.present_before IS DISTINCT FROM EXCLUDED.present_after THEN EXCLUDED.present_after
    ELSE scan_catalog_changes.present_after END,
    change_mask=scan_catalog_changes.change_mask | EXCLUDED.change_mask,last_changed_at=CURRENT_TIMESTAMP
   WHERE scan_catalog_changes.classification IS NULL`, f.scanID)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
	}
}
