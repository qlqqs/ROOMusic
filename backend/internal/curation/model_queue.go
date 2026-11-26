package curation

import (
	"context"
	"time"

	"github.com/qlqq/roomusic/backend/internal/catalog"
)

// QueueRankSQL 共用发行排序投影 r、当前主题 s 与实例设置 settings。
const QueueRankSQL = `CASE WHEN s.manual_requested_at IS NOT NULL THEN 0
 WHEN r.queue_added_at>settings.queue_baseline_at AND COALESCE(s.attempts,0)=0 THEN 1
 WHEN r.queue_has_id THEN 2 WHEN r.queue_class IN ('has_gaps','insufficient') THEN 3 ELSE 4 END`

// SQL 分类与调度器的模型任务一一对应。
const modelKindSQL = `CASE WHEN p.kind='local_cleanup' THEN 'local' WHEN p.kind IN ('release_merge','verify_artist_credit','verify_artist_merge','verify_name') THEN 'verify'
 WHEN p.kind IN ('release_match','recording_match') THEN 'select' END`

const modelQueueFromSQL = ` FROM curation_pending_items p
 LEFT JOIN curation_work_states work ON
 (work.subject=CASE p.target_type WHEN 'artist' THEN 'artist_media' ELSE 'release' END AND work.target_id=COALESCE(p.artist_id,p.target_id))
 CROSS JOIN LATERAL (SELECT work.manual_requested_at, p.attempts) s
 LEFT JOIN curation_work_states r ON r.subject='release' AND r.target_id=COALESCE(p.target_id,work.queue_release_id)
 CROSS JOIN curation_settings settings
 WHERE settings.id=1 AND p.status IN ('waiting_model','waiting_route') AND ` + PendingTargetVisibleSQL + `
 AND (COALESCE(p.target_id,work.queue_release_id) IS NULL OR EXISTS (SELECT FROM curation_stable_releases stable
 WHERE stable.id=COALESCE(p.target_id,work.queue_release_id)))`

// 只有同目录的在途项与延后项全部允许时，才分派整目录。
func localDirectoryReadySQL(at, excluded string) string {
	return ` AND (p.kind<>'local_cleanup' OR p.single_request OR NOT EXISTS (
 SELECT FROM curation_pending_items peer
 WHERE peer.kind='local_cleanup' AND peer.evidence->>'directory_key'=p.evidence->>'directory_key'
 AND peer.status IN ('waiting_model','waiting_route','in_progress')
 AND (peer.status='in_progress' OR peer.next_attempt_at>` + at + ` OR peer.id=ANY(` + excluded + `::uuid[]))))`
}

func (w *Writer) ReadyModelKinds(ctx context.Context, now time.Time, targets []string, excluded []string) (map[string]bool, error) {
	rows, err := w.db.QueryContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`)
 SELECT DISTINCT `+modelKindSQL+modelQueueFromSQL+`
 AND p.target_type=ANY($1::text[]) AND (p.next_attempt_at IS NULL OR p.next_attempt_at<=$2) AND NOT (p.id=ANY($3::uuid[]))`+localDirectoryReadySQL("$2", "$3"), targets, now, excluded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	kinds := make(map[string]bool)
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return nil, err
		}
		kinds[kind] = true
	}
	return kinds, rows.Err()
}

// ModelItemCheckpoint 说明下一项是否可开始；到达日额度时携带下次可查时间。
type ModelItemCheckpoint struct {
	Reason  string
	RetryAt *time.Time
}

func (w *Writer) BeforeModelItem(ctx context.Context, actor string, now time.Time) (ModelItemCheckpoint, error) {
	return w.BeforeModelKind(ctx, actor, now, "")
}

func (w *Writer) BeforeModelKind(ctx context.Context, actor string, now time.Time, kind string) (ModelItemCheckpoint, error) {
	settings, err := w.Settings(ctx)
	if err != nil {
		return ModelItemCheckpoint{}, err
	}
	if !settings.Active || settings.EnabledBy.ID != actor {
		return ModelItemCheckpoint{Reason: "disabled"}, nil
	}
	if settings.Paused {
		return ModelItemCheckpoint{Reason: "paused"}, nil
	}
	return w.CheckDailyBudget(ctx, now, settings.DailyTokenLimit)
}

func (w *Writer) CheckDailyBudget(ctx context.Context, now time.Time, limit *int64) (ModelItemCheckpoint, error) {
	if limit == nil {
		return ModelItemCheckpoint{}, nil
	}
	today, err := w.TodayUsage(ctx, now)
	if err != nil {
		return ModelItemCheckpoint{}, err
	}
	if today.AtLimit(limit) {
		next := NextBudgetDay(now)
		return ModelItemCheckpoint{Reason: "limit_reached", RetryAt: &next}, nil
	}
	return ModelItemCheckpoint{}, nil
}
