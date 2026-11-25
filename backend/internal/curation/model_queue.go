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

// 只有已经进入 waiting_model 的项才能交给当前模型处理；E 自行决定何时启用待路由项。
const modelKindSQL = `CASE WHEN p.route='lookup' THEN 'lookup'
 WHEN p.route='web' OR p.kind='artist_identity' THEN 'web'
 WHEN p.kind='release_merge' THEN 'verify'
 WHEN p.kind IN ('release_match','recording_match') THEN 'select' END`

const modelQueueFromSQL = ` FROM curation_pending_items p
 LEFT JOIN curation_work_states work ON
 (work.subject=CASE p.target_type WHEN 'artist' THEN 'artist_media' ELSE 'release' END AND work.target_id=COALESCE(p.artist_id,p.target_id))
 CROSS JOIN LATERAL (SELECT work.manual_requested_at, p.attempts) s
 LEFT JOIN curation_work_states r ON r.subject='release' AND r.target_id=COALESCE(p.target_id,work.queue_release_id)
 CROSS JOIN curation_settings settings
 WHERE settings.id=1 AND p.status='waiting_model' AND ` + PendingTargetVisibleSQL + `
 AND (p.kind<>'artist_identity' OR NOT EXISTS (` + ArtistReleaseCreditsSQL + `
 SELECT FROM credits c JOIN visible v ON v.id=c.release_id LEFT JOIN curation_work_states recognition
 ON recognition.subject='release' AND recognition.target_id=c.release_id
 WHERE c.artist_id=p.artist_id AND ` + ReleaseRecognitionPendingSQL + `))
 AND (COALESCE(p.target_id,work.queue_release_id) IS NULL OR EXISTS (SELECT FROM curation_stable_releases stable
 WHERE stable.id=COALESCE(p.target_id,work.queue_release_id)))`

func (w *Writer) ReadyModelKinds(ctx context.Context, now time.Time, targets []string) (map[string]bool, error) {
	rows, err := w.db.QueryContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`)
 SELECT DISTINCT `+modelKindSQL+modelQueueFromSQL+`
 AND p.target_type=ANY($1::text[]) AND (p.next_attempt_at IS NULL OR p.next_attempt_at<=$2)`, targets, now)
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

// LookupModelTargets 按同一队列顺序返回候选，调用方用巡检完成状态筛选。
func (w *Writer) LookupModelTargets(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := w.db.QueryContext(ctx, `WITH visible AS (`+catalog.VisibleReleaseIDsSQL+`)
 SELECT p.target_id::text `+modelQueueFromSQL+`
 AND p.route='lookup' AND p.target_type='release' AND (p.next_attempt_at IS NULL OR p.next_attempt_at<=$1)
 ORDER BY `+QueueRankSQL+`,p.created_at,p.id`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var targets []string
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

// ModelItemCheckpoint 说明下一项是否可开始；到达日额度时携带下次可查时间。
type ModelItemCheckpoint struct {
	Reason  string
	RetryAt *time.Time
}

func (w *Writer) BeforeModelItem(ctx context.Context, actor string, now time.Time) (ModelItemCheckpoint, error) {
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
	if settings.DailyTokenLimit == nil {
		return ModelItemCheckpoint{}, nil
	}
	today, err := w.TodayUsage(ctx, now)
	if err != nil {
		return ModelItemCheckpoint{}, err
	}
	if today.InputTokens+today.OutputTokens >= *settings.DailyTokenLimit {
		y, m, d := now.Date()
		next := time.Date(y, m, d+1, 0, 0, 0, 0, now.Location())
		return ModelItemCheckpoint{Reason: "limit_reached", RetryAt: &next}, nil
	}
	return ModelItemCheckpoint{}, nil
}
