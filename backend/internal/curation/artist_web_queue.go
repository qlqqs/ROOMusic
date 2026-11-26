package curation

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/identity"
	"github.com/qlqq/roomusic/backend/internal/operations"
)

const ArtistWebRuleVersion = 1
const ArtistWebCallLimit = 10
const ArtistWebTokenLimit = 50000

type ArtistWebQueueStatus struct {
	State      string     `json:"state"`
	Generation int64      `json:"generation"`
	Total      int        `json:"total"`
	Queued     int        `json:"queued"`
	Running    int        `json:"running"`
	Processed  int        `json:"processed"`
	Configured bool       `json:"configured"`
	CanStart   bool       `json:"can_start"`
	CanStop    bool       `json:"can_stop"`
	RetryAt    *time.Time `json:"retry_at"`
}

type artistWebActorKey struct{}

func WithArtistWebActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, artistWebActorKey{}, actor)
}
func ArtistWebActor(ctx context.Context) string {
	actor, _ := ctx.Value(artistWebActorKey{}).(string)
	return actor
}

// 读取身份用的本地事实；排序和摘要都使用有效署名与可见发行。
const artistWebInputs = `WITH ` + catalog.ArtistMediaFamilyCTE + `, visible AS (` + catalog.VisibleReleaseIDsSQL + `),
credited AS (
 SELECT DISTINCT f.canonical_id artist, v.id release, c.track_id, (c.track_id IS NOT NULL)::int priority
 FROM artist_effective_credits c JOIN artist_media_family f USING(artist_id) ` + catalog.CurationCreditJoins + `
 LEFT JOIN tracks t ON t.id=c.track_id LEFT JOIN media m ON m.id=t.medium_id
 JOIN visible v ON v.id=coalesce(m.release_id,c.release_id)
 WHERE c.role IN ` + PrimaryArtistRolesSQL + ` AND ` + catalog.UsableCurationCredit + `
 AND (c.track_id IS NULL OR t.source_status='present')
 AND ($1='' OR f.canonical_id=NULLIF($1,'')::uuid)
), albums AS (
 SELECT c.artist,r.id,r.title,r.year,min(c.priority) priority,curation_release_file_digest(r.id) files,
 (SELECT jsonb_agg(jsonb_build_array(t.id,t.title,t.duration_seconds,t.disc_number,t.position) ORDER BY t.id)
  FROM track_effective t JOIN media m ON m.id=t.medium_id WHERE m.release_id=r.id AND t.source_status='present') tracks
 FROM credited c JOIN release_effective r ON r.id=c.release GROUP BY c.artist,r.id,r.title,r.year
), inputs AS (
 SELECT a.artist, min(a.priority) priority, jsonb_build_object(
 'names', ARRAY(SELECT DISTINCT name FROM (
 SELECT coalesce(p.effective_display_name,ar.initial_name) name FROM artists ar LEFT JOIN artist_profiles p ON p.artist_id=ar.id WHERE ar.id=a.artist
 UNION SELECT n.original_value FROM artist_media_family f JOIN artist_search_names n USING(artist_id) WHERE f.canonical_id=a.artist) names ORDER BY name),
 'albums',jsonb_agg(jsonb_build_object('id',a.id,'name',a.title,'year',a.year) ORDER BY a.id),
 'tracks',ARRAY(SELECT DISTINCT t.title FROM credited c JOIN media m ON m.release_id=c.release JOIN track_effective t ON t.medium_id=m.id
 WHERE c.artist=a.artist AND t.source_status='present' AND (c.track_id IS NULL OR c.track_id=t.id) ORDER BY t.title LIMIT 12)) evidence,
 jsonb_agg(jsonb_build_array(a.id,a.files,a.tracks) ORDER BY a.id) baseline
 FROM albums a WHERE NOT EXISTS(SELECT FROM artist_media_family f JOIN external_links l ON l.object_id=f.artist_id
 WHERE f.canonical_id=a.artist AND l.object_type='artist' AND l.source='musicbrainz') GROUP BY a.artist
) SELECT artist,priority,evidence,encode(sha256(convert_to(jsonb_build_array(artist,$2::int,evidence,baseline,
 (SELECT jsonb_build_array(search_source,searxng_url,search_key_last_four) FROM steward_user_settings WHERE user_id=$3))::text,'UTF8')),'hex')
 FROM inputs ORDER BY priority,artist`

func (w *Writer) StartArtistWeb(ctx context.Context, actor, artist string) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var gen int64
	err = tx.QueryRowContext(ctx, `SELECT generation FROM curation_artist_web_queue WHERE id FOR UPDATE`).Scan(&gen)
	if err != nil {
		return err
	}
	if artist != "" {
		canonical, err := effectiveArtist(ctx, tx, artist)
		if err != nil {
			return err
		}
		if canonical != artist {
			return operations.Conflict("artist_redirected", "艺人已并入其他身份")
		}
	}
	var pending bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_pending_items WHERE artist_web_generation=$1 AND status IN ('waiting_model','waiting_route','in_progress'))`, gen).Scan(&pending)
	if err != nil {
		return err
	}
	if !pending {
		gen++
	}
	rows, err := tx.QueryContext(ctx, artistWebInputs, artist, ArtistWebRuleVersion, actor)
	if err != nil {
		return err
	}
	type entry struct {
		p        PendingItem
		priority int
	}
	var entries []entry
	for rows.Next() {
		var e entry
		err = rows.Scan(&e.p.ArtistID, &e.priority, &e.p.Evidence, &e.p.InputDigest)
		if err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if artist != "" && len(entries) == 0 {
		var linked bool
		err = tx.QueryRowContext(ctx, artistLinkFamilyCTE+` SELECT EXISTS(SELECT FROM media_links WHERE source='musicbrainz')`, artist).Scan(&linked)
		if err != nil {
			return err
		}
		if linked {
			return operations.Conflict("artist_already_linked", "艺人已有 MusicBrainz 对应")
		}
		return operations.Conflict("artist_out_of_scope", "艺人没有在库主署名")
	}
	added := false
	for _, e := range entries {
		p := e.p
		var skip bool
		err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_pending_items p WHERE p.artist_id=$1 AND p.kind='artist_identity' AND
  ((p.input_digest=$2 AND p.artist_web_generation IS NOT NULL AND p.resolution IS DISTINCT FROM 'input_changed') OR p.status='in_progress' OR EXISTS(SELECT FROM curation_model_exceptions x WHERE x.pending_item_id=p.id AND x.status IN ('open','accepting') AND (p.artist_web_generation IS NULL OR p.input_digest=$2))))`, p.ArtistID, p.InputDigest).Scan(&skip)
		if err != nil {
			return err
		}
		if skip {
			continue
		}
		added = true
		p.TargetType, p.Kind, p.RuleVersion = "artist", "artist_identity", ArtistWebRuleVersion
		if err = UpsertPendingItem(ctx, tx, p); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET artist_web_generation=$2,artist_web_priority=$3,artist_web_actor=$4,item_tool_calls=0,item_tokens=0,attempts=0,next_attempt_at=NULL WHERE artist_id=$1 AND status='waiting_model' AND kind='artist_identity'`, p.ArtistID, gen, e.priority, actor)
		if err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE curation_pending_items SET artist_web_suspended=false WHERE artist_web_generation=$1 AND artist_web_suspended AND ($2='' OR artist_id=NULLIF($2,'')::uuid)`, gen, artist); err != nil {
		return err
	}
	if !pending && !added {
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `UPDATE curation_artist_web_queue SET actor_id=CASE WHEN state='running' AND generation=$1 THEN actor_id ELSE $2 END,
 started_at=CASE WHEN generation=$1 THEN started_at ELSE now() END,generation=$1,stopped_at=NULL,state='running' WHERE id`, gen, actor)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (w *Writer) StopArtistWeb(ctx context.Context) error {
	_, err := w.db.ExecContext(ctx, `WITH stopped AS (UPDATE curation_artist_web_queue SET state='stopped',stopped_at=now() WHERE state='running' RETURNING generation)
 UPDATE curation_pending_items p SET artist_web_suspended=true FROM stopped WHERE p.artist_web_generation=stopped.generation AND p.status IN ('waiting_model','waiting_route','in_progress')`)
	return err
}

func (w *Writer) ArtistWebQueue(ctx context.Context, configured bool, now time.Time) (q ArtistWebQueueStatus, err error) {
	var ready int
	err = w.db.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE NOT p.artist_web_suspended AND p.status IN ('waiting_model','waiting_route','in_progress')),q.generation,q.state,count(p.id),count(*) FILTER(WHERE p.status IN ('waiting_model','waiting_route')),count(*) FILTER(WHERE p.status='in_progress')
 FROM curation_artist_web_queue q LEFT JOIN curation_pending_items p ON p.artist_web_generation=q.generation GROUP BY q.id`).Scan(&ready, &q.Generation, &q.State, &q.Total, &q.Queued, &q.Running)
	if err != nil {
		return q, err
	}
	q.Processed = q.Total - q.Running - q.Queued
	if q.Running+q.Queued == 0 {
		q.State = "idle"
	} else if ready == 0 {
		q.State = "stopped"
	}
	q.Configured, q.CanStart, q.CanStop = configured, configured, q.State == "running"
	if q.CanStop {
		settings, err := w.Settings(ctx)
		if err != nil {
			return q, err
		}
		point, err := w.CheckDailyBudget(ctx, now, settings.DailyTokenLimit)
		if err != nil {
			return q, err
		}
		q.RetryAt = point.RetryAt
	}
	return q, nil
}

func (w *Writer) ReadyArtistWebActor(ctx context.Context, now time.Time) (string, error) {
	var actor string
	err := w.db.QueryRowContext(ctx, `SELECT p.artist_web_actor FROM curation_artist_web_queue q
 JOIN curation_pending_items p ON p.artist_web_generation=q.generation JOIN users u ON u.id=p.artist_web_actor
 WHERE q.state='running' AND NOT p.artist_web_suspended AND u.role='admin' AND u.disabled_at IS NULL AND p.status IN ('waiting_model','waiting_route') AND (next_attempt_at IS NULL OR next_attempt_at<=$1)
 ORDER BY p.artist_web_priority,p.artist_id LIMIT 1`, now).Scan(&actor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return actor, err
}
func checkArtistWebActor(ctx context.Context, tx *sql.Tx, actor string) error {
	admin, err := identity.IsEnabledAdmin(ctx, tx, actor)
	if err != nil {
		return err
	}
	if !admin {
		return operations.Conflict("artist_web_disabled", "发起查找的管理员已停用")
	}
	return nil
}

func CheckArtistWebInput(ctx context.Context, q catalog.Queryer, item PendingItem) error {
	if item.ArtistWebGeneration == nil {
		return nil
	}
	var actor string
	if err := q.QueryRowContext(ctx, `SELECT artist_web_actor FROM curation_pending_items WHERE id=$1`, item.ID).Scan(&actor); err != nil {
		return err
	}
	var id, digest string
	var priority int
	var evidence []byte
	err := q.QueryRowContext(ctx, artistWebInputs, item.ArtistID, ArtistWebRuleVersion, actor).Scan(&id, &priority, &evidence, &digest)
	if errors.Is(err, sql.ErrNoRows) || err == nil && digest != item.InputDigest {
		return operations.Conflict("item_stale", "艺人署名或专辑证据已变化")
	}
	return err
}

func (w *Writer) ArtistWebRunning(ctx context.Context, generation int64, item string) (bool, error) {
	var ok bool
	err := w.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT FROM curation_artist_web_queue q JOIN curation_pending_items p ON p.artist_web_generation=q.generation WHERE q.state='running' AND q.generation=$1 AND p.id=$2 AND NOT p.artist_web_suspended)`, generation, item).Scan(&ok)
	return ok, err
}
