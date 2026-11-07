package ingest

import (
	"context"
	"database/sql"
	"errors"
)

var errFeedbackLockSetChanged = errors.New("scan feedback release lock set changed")

// 临时表把最终全库对账的集合留在数据库侧；Go 不保留全库曲目或发行数组。
func createFeedbackTables(ctx context.Context, tx *sql.Tx) error {
	for _, statement := range []string{
		`CREATE TEMP TABLE scan_feedback_tracks(id uuid PRIMARY KEY) ON COMMIT DROP`,
		`CREATE TEMP TABLE scan_feedback_releases(id uuid PRIMARY KEY) ON COMMIT DROP`,
		`CREATE TEMP TABLE scan_feedback_groups(id uuid PRIMARY KEY) ON COMMIT DROP`,
		`CREATE TEMP TABLE scan_feedback_before(kind text NOT NULL,id uuid NOT NULL,present boolean NOT NULL,
   metadata text NOT NULL,location text NOT NULL,release_id uuid,group_id uuid,artwork text NOT NULL,PRIMARY KEY(kind,id)) ON COMMIT DROP`,
		`CREATE TEMP TABLE scan_feedback_after(LIKE scan_feedback_before INCLUDING ALL) ON COMMIT DROP`,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
func collectFeedbackReleases(ctx context.Context, tx *sql.Tx, releaseID string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO scan_feedback_releases(id)
  SELECT m.release_id FROM tracks t JOIN media m ON m.id=t.medium_id JOIN scan_feedback_tracks s ON s.id=t.id
  UNION SELECT id FROM releases WHERE id=$1::uuid ON CONFLICT DO NOTHING`, nullableString(releaseID))
	return err
}

func lockFeedbackGraph(ctx context.Context, tx *sql.Tx) error {
	// 聚合只返回一个计数，锁集合按 UUID 升序在服务端消费。不可把关联读取合进此语句。
	if _, err := tx.ExecContext(ctx, `SELECT COUNT(*) FROM (SELECT r.id FROM releases r
  JOIN scan_feedback_releases s ON s.id=r.id ORDER BY r.id FOR UPDATE OF r) locked`); err != nil {
		return err
	}
	var changed bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tracks t JOIN media m ON m.id=t.medium_id
  JOIN scan_feedback_tracks s ON s.id=t.id WHERE NOT EXISTS(SELECT 1 FROM scan_feedback_releases r WHERE r.id=m.release_id))`).Scan(&changed); err != nil {
		return err
	}
	if changed {
		// 集合失效时不追加锁、不重试：回滚当前反馈尝试后立即降级，保持核心路径。
		return errFeedbackLockSetChanged
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scan_feedback_groups(id)
  SELECT DISTINCT r.group_id FROM releases r JOIN scan_feedback_releases s ON s.id=r.id`); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `SELECT COUNT(*) FROM (SELECT g.id FROM release_groups g
  JOIN scan_feedback_groups s ON s.id=g.id ORDER BY g.id FOR SHARE OF g) locked`)
	return err
}

// 投影列显式列举。歌词只保留数据库内的比较摘要，不复制到 Go 或历史表；
// 观察时间、指纹回填、文件大小/mtime、Medium UUID 与人工字段不进入业务投影。
func snapshotFeedbackGraph(ctx context.Context, tx *sql.Tx, after bool) error {
	table := "scan_feedback_before"
	if after {
		table = "scan_feedback_after"
	}
	statements := []string{
		`INSERT INTO ` + table + `(kind,id,present,metadata,location,release_id,group_id,artwork)
  SELECT 'track',t.id,t.source_status='present',md5(jsonb_build_array(
   t.title,t.artist,t.position,t.disc_number,COALESCE(t.duration_seconds,0),COALESCE(t.codec,''),COALESCE(t.sample_rate,0),COALESCE(t.channels,0),COALESCE(t.bitrate,0),COALESCE(t.bit_depth,0),
   t.source_kind,t.cue_index_frames,t.cue_end_frames,COALESCE(t.cue_isrc,''),COALESCE(t.lyrics,''),COALESCE(t.lyrics_source,''),
   COALESCE((SELECT jsonb_agg(jsonb_build_array(c.role,c.name) ORDER BY c.role,c.name) FROM track_credits c WHERE c.track_id=t.id),'[]'::jsonb))::text),
   md5(jsonb_build_array(t.source_root_id,t.relative_path,t.cue_sheet_path,t.cue_parent_relative_path,t.cue_referenced_file)::text),
   m.release_id,r.group_id,'' FROM tracks t JOIN scan_feedback_tracks s ON s.id=t.id
   JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id`,
		`INSERT INTO ` + table + `(kind,id,present,metadata,location,release_id,group_id,artwork)
  SELECT 'release',r.id,EXISTS(SELECT 1 FROM media m JOIN tracks t ON t.medium_id=m.id WHERE m.release_id=r.id AND t.source_status='present'),
   md5(jsonb_build_array(r.title,r.artist,COALESCE(r.album_artist,''),COALESCE(r.year,0),COALESCE(r.source_type,''),COALESCE(r.media_type,''),to_jsonb(r.genres),COALESCE(r.catalog_number,''),COALESCE(r.edition,''),COALESCE(r.label,''),COALESCE(r.barcode,''),
   COALESCE((SELECT jsonb_agg(jsonb_build_array(c.role,c.name) ORDER BY c.role,c.name) FROM release_credits c WHERE c.release_id=r.id),'[]'::jsonb))::text),
   md5(jsonb_build_array(r.source_root_id,r.relative_directory,r.candidate_kind)::text),r.id,r.group_id,
   COALESCE((SELECT storage_key FROM release_artworks a WHERE a.release_id=r.id),'')
   FROM releases r JOIN scan_feedback_releases s ON s.id=r.id`,
		`INSERT INTO ` + table + `(kind,id,present,metadata,location,release_id,group_id,artwork)
  SELECT 'album',g.id,EXISTS(SELECT 1 FROM releases r JOIN media m ON m.release_id=r.id JOIN tracks t ON t.medium_id=m.id
   WHERE r.group_id=g.id AND t.source_status='present'),'','',NULL,g.id,''
   FROM release_groups g JOIN scan_feedback_groups s ON s.id=g.id`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}
