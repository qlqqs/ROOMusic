package program

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/qlqq/roomusic/backend/internal/curation"
)

type queueItem struct {
	PatrolRevision                            int64
	Subject, TargetID, ReleaseID, InputDigest string
	Files                                     string
	Rank, Generation                          int
	ManualRequestedAt                         *time.Time
}

// 发行投影保存排序依据，各媒体主题共享这些依据并独立消费自己的首次机会。
const queueRankSQL = `CASE WHEN s.subject IN ('release','fingerprint') AND s.manual_requested_at IS NULL
 AND r.queue_class='local_complete' AND NOT r.queue_has_id THEN 4 ELSE ` + curation.QueueRankSQL + ` END`

const queueCandidatesSQL = `WITH visible AS NOT MATERIALIZED (SELECT id FROM curation_eligible_releases),
 candidates AS (
 SELECT work.target_id,work.queue_release_id,r.queue_identity,r.generation,work.manual_requested_at,work.patrol_revision,
 ` + queueRankSQL + ` rank,r.queue_gap,r.queue_artist_unlinked,r.queue_added_at,
 (work.queue_release_id IS NULL OR stable.id IS NOT NULL) stable,
 (work.needs_work OR work.next_attempt_at<=$2) due,
 work.result->'retry'->>'source' retry_source,work.result->'retry'->>'reason' retry_reason,work.next_attempt_at,
 (work.subject<>'release' OR NOT EXISTS (SELECT FROM curation_work_states fp
 WHERE fp.subject='fingerprint' AND fp.needs_work AND fp.queue_release_id=r.target_id
 AND fp.generation=r.generation AND fp.input_digest=r.queue_identity
 AND EXISTS (SELECT FROM tracks t JOIN media m ON m.id=t.medium_id WHERE t.id=fp.target_id
 AND t.source_status='present' AND m.release_id=r.target_id))) dependencies_ready
 FROM curation_work_states work LEFT JOIN curation_work_states r ON r.subject='release' AND r.target_id=work.queue_release_id
 CROSS JOIN LATERAL (SELECT work.subject,
     CASE WHEN work.subject='fingerprint' THEN r.manual_requested_at ELSE work.manual_requested_at END manual_requested_at,
     CASE WHEN work.subject='fingerprint' THEN r.attempts ELSE work.attempts END attempts) s
 CROSS JOIN curation_settings settings
 LEFT JOIN visible v ON v.id=work.queue_release_id
 LEFT JOIN curation_stable_releases stable ON stable.id=work.queue_release_id
 WHERE work.subject=$1 AND settings.id=1
 AND (work.needs_work OR (work.queue_release_id IS NOT NULL AND (work.status IN ('try_later','unmatched') AND work.next_attempt_at<=$2 OR work.subject='release' AND work.status='try_later' AND work.result ? 'retry')))
 AND (v.id IS NOT NULL OR (work.subject='artist_media' AND work.queue_release_id IS NULL))
 AND (work.subject NOT IN ('lyrics','fingerprint') OR EXISTS (SELECT FROM tracks t JOIN media m ON m.id=t.medium_id
 WHERE t.id=work.target_id AND t.source_status='present' AND m.release_id=work.queue_release_id))
 AND (work.subject<>'fingerprint' OR (work.generation=r.generation AND work.input_digest=r.queue_identity))
 ) `

func (w *Worker) nextQueueItem(ctx context.Context, subject string) (queueItem, bool, error) {
	item := queueItem{Subject: subject}
	err := w.db.QueryRowContext(ctx, queueCandidatesSQL+`SELECT target_id,COALESCE(queue_release_id::text,''),
 rank,COALESCE(generation,0),COALESCE(queue_identity,''),manual_requested_at,patrol_revision,CASE WHEN $1 IN ('release','fingerprint') THEN COALESCE(curation_release_file_digest(candidates.queue_release_id),'') ELSE '' END FROM candidates WHERE stable AND due AND dependencies_ready
 ORDER BY rank,CASE WHEN rank=3 THEN queue_gap END DESC,
 CASE WHEN rank=4 THEN queue_artist_unlinked END DESC NULLS LAST,queue_added_at DESC NULLS LAST,target_id LIMIT 1`,
		subject, w.opts.Now()).Scan(&item.TargetID, &item.ReleaseID, &item.Rank, &item.Generation, &item.InputDigest, &item.ManualRequestedAt, &item.PatrolRevision, &item.Files)
	if errors.Is(err, sql.ErrNoRows) {
		return item, false, nil
	}
	return item, err == nil, err
}

func (w *Worker) queueCounts(ctx context.Context, subject string) (remaining, ready, stable int64, err error) {
	err = w.db.QueryRowContext(ctx, queueCandidatesSQL+`SELECT count(*),count(*) FILTER (WHERE stable AND due AND dependencies_ready),count(*) FILTER (WHERE stable) FROM candidates`, subject, w.opts.Now()).Scan(&remaining, &ready, &stable)
	return
}

func (w *Worker) hasHigherIdentity(ctx context.Context, now time.Time) (bool, error) {
	var high bool
	err := w.db.QueryRowContext(ctx, queueCandidatesSQL+`SELECT EXISTS (SELECT FROM candidates WHERE stable AND due AND rank<4)`, "release", now).Scan(&high)
	return high, err
}

func (w *Worker) acknowledgeQueueItem(ctx context.Context, item queueItem) error {
	if item.Subject == "fingerprint" {
		return nil
	}
	_, err := w.db.ExecContext(ctx, `UPDATE curation_work_states SET needs_work=false,manual_requested_at=NULL
 WHERE subject=$1 AND target_id=$2 AND needs_work AND manual_requested_at IS NOT DISTINCT FROM $3::timestamptz AND patrol_revision=$4`,
		item.Subject, item.TargetID, item.ManualRequestedAt, item.PatrolRevision)
	return err
}

// 抽样需求跟随产生它的发行识别依据。重启后可直接恢复等待中的发行和未计算曲目。
func (w *Worker) queueFingerprints(ctx context.Context, item queueItem, trackIDs []string) error {
	if len(trackIDs) == 0 {
		return nil
	}
	result, err := w.db.ExecContext(ctx, `INSERT INTO curation_work_states AS s(subject,target_id,rule_version,input_digest,status,generation,queue_release_id,needs_work)
 SELECT 'fingerprint',t.id,$3,r.queue_identity,'pending',r.generation,r.target_id,true
 FROM curation_work_states r JOIN media m ON m.release_id=r.target_id JOIN tracks t ON t.medium_id=m.id
 WHERE r.subject='release' AND r.target_id=$1 AND t.id=ANY($2::uuid[]) AND t.source_status='present'
 AND r.generation=$4 AND r.queue_identity=$5
 ON CONFLICT(subject,target_id) DO UPDATE SET rule_version=excluded.rule_version,input_digest=excluded.input_digest,
 generation=excluded.generation,queue_release_id=excluded.queue_release_id,status='pending',needs_work=true`, item.ReleaseID, trackIDs, resourceRuleVersion, item.Generation, item.InputDigest)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != int64(len(trackIDs)) {
		return errReleaseChanged
	}
	return err
}

func (w *Worker) finishFingerprint(ctx context.Context, item queueItem) error {
	_, err := w.db.ExecContext(ctx, `UPDATE curation_work_states SET status='done',needs_work=false,updated_at=now()
 WHERE subject='fingerprint' AND target_id=$1 AND generation=$2 AND input_digest=$3 AND queue_release_id=$4 AND needs_work`, item.TargetID, item.Generation, item.InputDigest, item.ReleaseID)
	return err
}
