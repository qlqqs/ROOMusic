package curation

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/operations"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// 目录只读可见、稳定的署名。专辑采用人工标题或扫描标题，过滤扫描占位来源。
const patrolFacts = `, scoped AS NOT MATERIALIZED (
 SELECT c.*,r.id AS album_id FROM scope r JOIN artist_effective_credits c ON c.release_id=r.id
 UNION ALL SELECT c.*,r.id FROM scope r JOIN media m ON m.release_id=r.id
 JOIN tracks t ON t.medium_id=m.id AND t.source_status='present' JOIN artist_effective_credits c ON c.track_id=t.id
 ), credits AS (
 SELECT c.album_id AS release_id,c.artist_id,spelling.spelling AS name,c.track_id,c.role,c.position
 FROM scoped c ` + catalog.CurationCreditJoins + `
 WHERE c.role NOT IN ` + PrimaryArtistRolesSQL + ` OR ` + catalog.UsableCurationCredit + `
 ), entries AS (
 SELECT DISTINCT r.id AS release_id,c.artist_id,c.name,
 CASE WHEN tc.text_value IS NOT NULL THEN tc.text_value
 WHEN COALESCE(d.selected_source,'') NOT IN ('default_fallback','display_fallback')
 AND (r.title<>'未知专辑' OR d.selected_source IS NOT NULL) THEN r.title ELSE '' END AS title
 FROM scope s JOIN releases r ON r.id=s.id LEFT JOIN credits c ON c.release_id=s.id
 LEFT JOIN release_corrections tc ON tc.release_id=r.id AND tc.field='title'
 LEFT JOIN release_field_decisions d ON d.release_id=r.id AND d.field_key='title'
 ), names AS (
 SELECT DISTINCT e.artist_id,e.release_id,n.kind,n.name,
 encode(sha256(convert_to(n.kind||'/'||COALESCE(e.artist_id,e.release_id)::text||'/'||n.name,'UTF8')),'hex') AS id
 FROM entries e CROSS JOIN LATERAL (VALUES('artist',e.name),('album',e.title)) n(kind,name)
 WHERE n.name<>''
 ) `

const patrolRelease = `WITH visible AS NOT MATERIALIZED (` + catalog.VisibleReleaseIDsSQL + `),
 scope AS (SELECT v.id FROM visible v JOIN curation_stable_releases s USING(id) WHERE v.id=$1)` + patrolFacts

// 页面内的名字与归属投影供调度按索引领取；扫描稳定与否在领取时实时判断。
func ProjectPatrolInputs(ctx context.Context, tx *sql.Tx, ids []string) error {
	const inputs = `WITH scope AS (SELECT unnest($1::uuid[]) AS id)` + patrolFacts
	// 同一条语句比较集合并同步归属，盘点已有页面时不重写相同数据。
	_, err := tx.ExecContext(ctx, inputs+`, removed AS (
 DELETE FROM curation_patrol_inputs old WHERE old.release_id=ANY($1::uuid[])
 AND NOT EXISTS(SELECT FROM names n WHERE n.release_id=old.release_id AND n.id=old.name_id)),
 added AS (
 INSERT INTO curation_patrol_names(id,kind,artist_id,name,original_name,latin_name,original_key,latin_key,confidence,issues,model,prompt_version,patrolled_at,needs_patrol)
 SELECT n.id,n.kind,n.artist_id,n.name,COALESCE(old.original_name,''),COALESCE(old.latin_name,''),COALESCE(old.original_key,''),COALESCE(old.latin_key,''),
 old.confidence,COALESCE(old.issues,'[]'),old.model,old.prompt_version,old.patrolled_at,COALESCE(old.needs_patrol,true)
 FROM (SELECT DISTINCT id,kind,artist_id,name FROM names) n
 LEFT JOIN LATERAL (
 SELECT p.* FROM artist_overrides o JOIN curation_patrol_names p ON p.artist_id=o.artist_id AND p.kind=n.kind AND p.name=n.name
 WHERE o.redirect_artist_id=n.artist_id AND p.patrolled_at IS NOT NULL ORDER BY p.patrolled_at DESC,p.id LIMIT 1
 ) old ON true WHERE NOT EXISTS(SELECT FROM curation_patrol_names current WHERE current.id=n.id)
 ON CONFLICT(id) DO NOTHING)
 INSERT INTO curation_patrol_inputs(release_id,name_id) SELECT release_id,id FROM names
 ON CONFLICT DO NOTHING`, ids)
	return err
}

const patrolMembership = "WITH " + patrolMembershipCTEs
const patrolMembershipCTEs = `visible AS NOT MATERIALIZED (` + catalog.VisibleReleaseIDsSQL + `),
 members AS NOT MATERIALIZED (
 SELECT i.release_id,i.name_id FROM curation_patrol_inputs i JOIN curation_stable_releases s ON s.id=i.release_id
 JOIN visible v ON v.id=i.release_id)`

type patrolName struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	ArtistID string   `json:"artist_id"`
	Artist   string   `json:"artist"`
	Albums   []string `json:"albums,omitempty"`
}
type patrolBatch struct {
	Kind  string       `json:"kind"`
	Names []patrolName `json:"names"`
}

const pendingPatrol = patrolMembership + `, pending AS NOT MATERIALIZED (
 SELECT n.id,n.kind,n.name,n.artist_id,m.release_id FROM curation_patrol_names n
 JOIN LATERAL (SELECT release_id FROM members WHERE name_id=n.id ORDER BY release_id LIMIT 1) m ON true
 WHERE n.needs_patrol AND n.pending_id IS NULL
 ), seed AS (SELECT kind,artist_id,release_id FROM pending ORDER BY kind DESC,artist_id,id LIMIT 1),
 batch AS (SELECT p.* FROM pending p JOIN seed USING(kind)
 WHERE p.kind='artist' OR p.artist_id=seed.artist_id OR (p.artist_id IS NULL AND p.release_id=seed.release_id)
 ORDER BY p.artist_id,p.id LIMIT 32)
 SELECT b.kind,b.id,b.name,COALESCE(b.artist_id::text,''),COALESCE(p.effective_display_name,a.initial_name,''),b.release_id,
 to_json(ARRAY(SELECT ref.name FROM curation_patrol_names ref
 WHERE ref.kind='album' AND ref.artist_id=b.artist_id AND ref.id<>b.id
 AND (b.kind='artist' OR ref.patrolled_at IS NOT NULL)
 AND EXISTS(SELECT FROM members m WHERE m.name_id=ref.id)
 ORDER BY CASE WHEN b.kind='album' THEN public.similarity(ref.name,b.name) END DESC,ref.name
 LIMIT CASE WHEN b.kind='artist' THEN 3 ELSE 6 END))
 FROM batch b LEFT JOIN artists a ON a.id=b.artist_id LEFT JOIN artist_profiles p ON p.artist_id=b.artist_id ORDER BY b.id`

// 盘点、提交和手工重巡共用事务锁，以免不同会话同时分派同一个名字。
func patrolLock(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('roomusic/curation/name-patrol',0))`)
	return err
}

func (w *Writer) MaterializePatrol(ctx context.Context, version int16) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = patrolLock(ctx, tx); err != nil {
		return err
	}
	// 扫描移除了代表发行时，释放同批仍在库的其他名字，允许换一个锚点。
	_, err = tx.ExecContext(ctx, patrolMembership+`, retired AS (
 UPDATE curation_pending_items p SET status='resolved',resolution='stale',resolved_at=now(),updated_at=now()
 WHERE p.kind='name_patrol' AND p.status<>'resolved' AND
 (NOT EXISTS(SELECT FROM visible WHERE id=p.target_id) OR EXISTS (
 SELECT FROM curation_patrol_names n WHERE n.pending_id=p.id
 AND NOT EXISTS(SELECT FROM members m WHERE m.name_id=n.id)))
 RETURNING p.id) UPDATE curation_patrol_names SET pending_id=NULL WHERE pending_id IN (SELECT id FROM retired)`)
	if err != nil {
		return err
	}
	// 待发批次使用本次实际发送的提示词版本，已经提交的名字继续沿用其原版本。
	if _, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET rule_version=$1 WHERE kind='name_patrol' AND status='waiting_model' AND rule_version<>$1`, version); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, pendingPatrol)
	if err != nil {
		return err
	}
	var batch patrolBatch
	var anchor string
	var payloadBytes int
	for rows.Next() {
		var n patrolName
		var release string
		var albums []byte
		if err = rows.Scan(&batch.Kind, &n.ID, &n.Name, &n.ArtistID, &n.Artist, &release, &albums); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(albums, &n.Albums); err != nil {
			rows.Close()
			return err
		}
		// 同一行的多条长标题也计入管家首条消息的 1 MiB 边界。
		encoded, err := json.Marshal(n)
		if err != nil {
			rows.Close()
			return err
		}
		if len(batch.Names) > 0 && payloadBytes+len(encoded) > 900<<10 {
			break
		}
		payloadBytes += len(encoded)
		if anchor == "" {
			anchor = release
		}
		batch.Names = append(batch.Names, n)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if len(batch.Names) == 0 {
		return tx.Commit()
	}
	id := uuid.New()
	raw, err := json.Marshal(batch)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO curation_pending_items(id,target_type,target_id,kind,status,rule_version,input_digest,evidence)
 VALUES($1,'release',$2,'name_patrol','waiting_model',$3,$4,$5)`, id, anchor, version, operations.Fingerprint(batch), raw)
	if err != nil {
		return err
	}
	for _, n := range batch.Names {
		_, err = tx.ExecContext(ctx, `UPDATE curation_patrol_names SET pending_id=$2 WHERE id=$1`, n.ID, id)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (w *Writer) Repatrol(ctx context.Context) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = patrolLock(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET status='resolved',resolution='repatrol',resolved_at=now(),
 updated_at=now(),claimed_at=NULL,next_attempt_at=NULL WHERE kind='name_patrol' AND status<>'resolved'`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE curation_patrol_names SET pending_id=NULL,needs_patrol=true`); err != nil {
		return err
	}
	return tx.Commit()
}

func (w *Writer) NamesPatrolled(ctx context.Context, releaseID string) (bool, error) {
	var done bool
	err := w.db.QueryRowContext(ctx, patrolRelease+`SELECT EXISTS(SELECT FROM scope)
 AND NOT EXISTS(SELECT FROM names n LEFT JOIN curation_patrol_names p ON p.id=n.id WHERE n.release_id=$1 AND COALESCE(p.needs_patrol,true))`, releaseID).Scan(&done)
	return done, err
}

// 只替换当前署名。合署名按原来的位置重组，专辑建议不能误用为散曲标题。
func (w *Writer) CanonicalPatrolNames(ctx context.Context, releaseID, trackID string) (title, artist string, err error) {
	err = w.db.QueryRowContext(ctx, patrolRelease+`, selected AS (
 SELECT c.*,dense_rank() OVER (ORDER BY CASE c.role WHEN 'album_artist' THEN 1 WHEN 'release_artist' THEN 2 ELSE 3 END) priority
 FROM credits c WHERE c.release_id=$1 AND (
 ($2='' AND c.role IN ('album_artist','release_artist')) OR (c.role='track_artist' AND c.track_id=NULLIF($2,'')::uuid))
 ), canonical AS (
 SELECT s.position,s.name,CASE WHEN p.confidence='high' AND p.patrolled_at IS NOT NULL THEN p.original_name ELSE s.name END AS replacement
 FROM selected s JOIN names n ON n.artist_id=s.artist_id AND n.name=s.name AND n.release_id=s.release_id AND n.kind='artist'
 LEFT JOIN curation_patrol_names p ON p.id=n.id WHERE priority=1
 ) SELECT COALESCE((SELECT min(p.original_name) FROM names n JOIN curation_patrol_names p ON n.id=p.id
 WHERE n.release_id=$1 AND $2='' AND n.kind='album' AND p.original_name<>'' AND p.patrolled_at IS NOT NULL
 HAVING count(DISTINCT p.original_name)=1),''),
 COALESCE((SELECT string_agg(replacement,' / ' ORDER BY position,name) FROM canonical HAVING bool_or(replacement<>name)),'')`, releaseID, trackID).Scan(&title, &artist)
	return
}
