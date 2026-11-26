package curation

import (
	"context"
	"database/sql"
	"time"
)

// 领取和重审使用同一组互斥键。巡检块可能含多个艺人，发行并入涉及两张发行。
const modelItemsSQL = `WITH items AS (
 SELECT id,target_id,artist_id,kind,
 CASE WHEN kind='release_merge' THEN candidates->0->>'release_id' END AS merge_release,
 CASE WHEN kind='verify_artist_merge' THEN evidence->>'artist_a' END AS artist_a,
 CASE WHEN kind='verify_artist_merge' THEN evidence->>'artist_b' END AS artist_b
 FROM curation_pending_items WHERE `

const modelTargetKeysSQL = `, releases_for_item AS (
 SELECT id,target_id AS release_id FROM items WHERE target_id IS NOT NULL
 UNION SELECT p.id,r.id FROM items p JOIN releases r ON r.id::text=p.merge_release
 UNION SELECT n.pending_id,i.release_id FROM items p JOIN curation_patrol_names n ON n.pending_id=p.id
 JOIN curation_patrol_inputs i ON i.name_id=n.id
 ), credits AS (` + ArtistReleaseCreditsSQL + ` SELECT * FROM credits),
 target_keys AS (
 SELECT id,'release/'||release_id::text AS key FROM releases_for_item
 UNION ALL SELECT f.id,'artist/'||c.artist_id::text FROM releases_for_item f JOIN credits c USING(release_id)
 UNION ALL SELECT f.id,'name/'||lower(btrim(COALESCE(NULLIF(r.album_artist,''),r.artist)))
 FROM releases_for_item f JOIN releases r ON r.id=f.release_id WHERE COALESCE(NULLIF(r.album_artist,''),r.artist)<>''
 UNION ALL SELECT id,'artist/'||artist_id::text FROM items WHERE artist_id IS NOT NULL
 UNION ALL SELECT id,'artist/'||v.artist FROM items CROSS JOIN LATERAL
 (VALUES(artist_a),(artist_b)) v(artist) WHERE kind='verify_artist_merge'
 UNION ALL SELECT p.id,'artist/'||n.artist_id::text FROM items p JOIN curation_patrol_names n ON n.pending_id=p.id
 ) `

func (w *Writer) ModelTargetKeys(ctx context.Context, ids []string) ([]string, error) {
	return modelStrings(w.db.QueryContext(ctx, modelItemsSQL+`id=ANY($1::uuid[]))`+modelTargetKeysSQL+
		`SELECT DISTINCT key FROM target_keys WHERE key IS NOT NULL`, ids))
}

func (w *Writer) ConflictingModelItems(ctx context.Context, keys []string, now time.Time) ([]string, error) {
	if len(keys) == 0 {
		return []string{}, nil
	}
	return modelStrings(w.db.QueryContext(ctx, modelItemsSQL+`status IN ('waiting_model','waiting_route','in_progress') AND (next_attempt_at IS NULL OR next_attempt_at<=$2))`+modelTargetKeysSQL+
		`SELECT DISTINCT id::text FROM target_keys WHERE key=ANY($1::text[])`, keys, now))
}

func modelStrings(rows *sql.Rows, err error) ([]string, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}
