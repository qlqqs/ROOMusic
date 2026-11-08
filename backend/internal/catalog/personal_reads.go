package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

const (
	maxFactTrackIDs     = 10000
	maxPersonalGroupIDs = 1000
)

// TrackFact 是当前曲目身份、位置、可用性和安全时长，不包含发行摘要或存储身份。
type TrackFact struct {
	TrackID         string   `json:"track_id"`
	ReleaseID       string   `json:"release_id"`
	GroupID         string   `json:"group_id"`
	Present         bool     `json:"present"`
	DurationSeconds *float64 `json:"duration_seconds"`
}

// PersonalReleaseGroupSummary 保留不可用的专辑身份；不改变 ReleaseGroupSummary 的非空代表合同。
type PersonalReleaseGroupSummary struct {
	GroupID        string          `json:"group_id"`
	Available      bool            `json:"available"`
	ReleaseCount   int             `json:"release_count"`
	Representative *ReleaseSummary `json:"representative"`
}

const loadTrackFactsSQL = `SELECT t.id::text, m.release_id::text, r.group_id::text,
 t.source_status='present', dd.duration_seconds
 FROM tracks t JOIN media m ON m.id=t.medium_id JOIN releases r ON r.id=m.release_id
 LEFT JOIN track_display_durations dd ON dd.track_id=t.id
 WHERE t.id=ANY($1::text[]::uuid[])`

// LoadTrackFacts 批量读取至多 10,000 个不同有效 UUID，包含 missing；不存在者无键。
// 无效 UUID 跳过、小写去重；空输入不查库。调用者可用 NewStore(tx) 共享自己的快照。
func (s *Store) LoadTrackFacts(ctx context.Context, trackIDs []string) (map[string]TrackFact, error) {
	ids, err := personalReadIDs(trackIDs, maxFactTrackIDs)
	if err != nil {
		return nil, fmt.Errorf("load track facts: %w", err)
	}
	result := make(map[string]TrackFact, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.database.QueryContext(ctx, loadTrackFactsSQL, ids)
	if err != nil {
		return nil, fmt.Errorf("%w: load track facts: %w", ErrQuery, err)
	}
	defer rows.Close()
	for rows.Next() {
		var item TrackFact
		var duration sql.NullFloat64
		if err := rows.Scan(&item.TrackID, &item.ReleaseID, &item.GroupID, &item.Present, &duration); err != nil {
			return nil, fmt.Errorf("load track facts: %w", err)
		}
		if duration.Valid && duration.Float64 > 0 && !math.IsNaN(duration.Float64) && !math.IsInf(duration.Float64, 0) {
			item.DurationSeconds = &duration.Float64
		}
		result[item.TrackID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load track facts: %w", err)
	}
	return result, nil
}

// 只对请求组的可见成员排名，且只为代表计算完整摘要；空组也保留在同一语句快照中。
// DISTINCT ON 保留完整窗口计数，也避免 member_rank=1 的低估导致重复扫描整个发行视图。
// LEFT JOIN 的空摘要仅用内部零值接收，release_count=0 时绝不构造或返回代表对象。
var loadPersonalReleaseGroupSummariesSQL = `WITH selected_groups AS MATERIALIZED (
 SELECT id FROM release_groups WHERE id=ANY($1::text[]::uuid[])
), representatives AS MATERIALIZED (
 SELECT DISTINCT ON (releases.group_id) releases.id, releases.group_id,
 COUNT(*) OVER (PARTITION BY releases.group_id) AS release_count
 FROM ` + releasesql.ReleaseSource + ` JOIN selected_groups ON selected_groups.id=releases.group_id
 WHERE ` + releasesql.ReleaseVisiblePredicate + `
 ORDER BY releases.group_id, ` + releasesql.ReleaseGroupMemberOrder + `
), summary_release_ids AS MATERIALIZED (
 SELECT id FROM representatives
), ` + releasesql.BatchSummaryCountsCTE + `, ` + releasesql.BatchSummaryArtistRefsCTE + `, summaries (group_id,release_count,id,title,artist,album_artist,year,source_type,media_type,medium_count,track_count,attention_count,artwork_key,artwork_mime,artwork_width,artwork_height,artist_refs,album_artist_refs) AS MATERIALIZED (
 SELECT representatives.group_id, representatives.release_count, ` + releasesql.BatchSummaryProjection + `
 FROM representatives JOIN ` + releasesql.ReleaseSource + ` ON releases.id=representatives.id` + releasesql.BatchSummaryJoins + `
)
SELECT g.id::text, COALESCE(s.release_count,0),
 COALESCE(s.id,''),COALESCE(s.title,''),COALESCE(s.artist,''),s.album_artist,s.year,s.source_type,s.media_type,
 COALESCE(s.medium_count,0),COALESCE(s.track_count,0),COALESCE(s.attention_count,0),
 s.artwork_key,s.artwork_mime,s.artwork_width,s.artwork_height,COALESCE(s.artist_refs,'[]'::jsonb),COALESCE(s.album_artist_refs,'[]'::jsonb)
FROM selected_groups g LEFT JOIN summaries s ON s.group_id=g.id`

// LoadReleaseGroupSummaries 批量读取至多 1,000 个不同有效 UUID；不存在者无键。
// 存在但没有可见成员时 Available=false、ReleaseCount=0、Representative=nil。
// 无效 UUID 跳过、小写去重；空输入不查库，代表排序、有效值和安全封面沿用目录公开读取规则。
func (s *Store) LoadReleaseGroupSummaries(ctx context.Context, groupIDs []string) (map[string]PersonalReleaseGroupSummary, error) {
	ids, err := personalReadIDs(groupIDs, maxPersonalGroupIDs)
	if err != nil {
		return nil, fmt.Errorf("load release group summaries: %w", err)
	}
	result := make(map[string]PersonalReleaseGroupSummary, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	rows, err := s.database.QueryContext(ctx, loadPersonalReleaseGroupSummariesSQL, ids)
	if err != nil {
		return nil, fmt.Errorf("%w: load release group summaries: %w", ErrQuery, err)
	}
	defer rows.Close()
	for rows.Next() {
		var item PersonalReleaseGroupSummary
		var columns ReleaseSummaryColumns
		targets := append([]any{&item.GroupID, &item.ReleaseCount}, columns.ScanTargets()...)
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("load release group summaries: %w", err)
		}
		if item.ReleaseCount > 0 {
			representative, err := columns.Summary()
			if err != nil {
				return nil, fmt.Errorf("load release group summaries: %w", err)
			}
			item.Available = true
			item.Representative = &representative
		}
		result[item.GroupID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load release group summaries: %w", err)
	}
	return result, nil
}

// personalReadIDs 与既有批量摘要一致：只限制去重后的有效 UUID；HTTP 必须自行拒绝非法输入。
func personalReadIDs(input []string, limit int) ([]string, error) {
	ids := make([]string, 0, min(len(input), limit))
	seen := make(map[string]bool, cap(ids))
	for _, id := range input {
		if !uuid.Valid(id) {
			continue
		}
		id = strings.ToLower(id)
		if seen[id] {
			continue
		}
		if len(ids) == limit {
			return nil, fmt.Errorf("more than %d distinct IDs", limit)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}
