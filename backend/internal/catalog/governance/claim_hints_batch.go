package governance

import (
	"context"
	"fmt"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/platform/uuid"
)

// LoadClaimHintsBatch 为报告当前页批量读取实时认领提示；每个发行最多三条，不受正向推荐五条的限制。
// 最多接受一百个 ID，非法 ID 报错，重复项归一；不存在、不可见或没有提示的发行返回空切片。
// 调用方负责管理员权限，并传入与历史报告读取相同的只读 REPEATABLE READ Queryer。
func (s *Store) LoadClaimHintsBatch(ctx context.Context, releaseIDs []string) (map[string][]ClaimHint, error) {
	if len(releaseIDs) > 100 {
		return nil, fmt.Errorf("claim hints batch exceeds bound")
	}
	hints := make(map[string][]ClaimHint, len(releaseIDs))
	ids := make([]string, 0, len(releaseIDs))
	for _, id := range releaseIDs {
		if !uuid.Valid(id) {
			return nil, fmt.Errorf("invalid claim hint release ID")
		}
		id = strings.ToLower(id)
		if _, exists := hints[id]; exists {
			continue
		}
		ids = append(ids, id)
		hints[id] = []ClaimHint{}
	}
	if len(ids) == 0 {
		return hints, nil
	}
	rows, err := s.database.QueryContext(ctx, `
  WITH targets AS (
   SELECT releases.id,releases.source_root_id,releases.relative_directory
   FROM `+releasesql.ReleaseSource+` WHERE releases.id=ANY($1::uuid[]) AND `+releasesql.ReleaseVisiblePredicate+`
  ), matches AS (
   SELECT target.id AS target_id,releases.id,releases.title,
    ROW_NUMBER() OVER (PARTITION BY target.id ORDER BY `+claimLastSeen+` DESC,releases.id) AS hint_rank
   FROM `+releasesql.ReleaseSource+`, targets target
   WHERE `+releaseClaimablePredicate+` AND (
    (releases.source_root_id=target.source_root_id AND releases.relative_directory=target.relative_directory)
    OR EXISTS (
     SELECT 1 FROM `+claimPathSetFor("releases.id")+` path_set
     JOIN tracks claim_track ON claim_track.source_root_id=path_set.root_id
      AND claim_track.relative_path=path_set.relative_path AND claim_track.source_identity IS NOT NULL
     JOIN media claim_medium ON claim_medium.id=claim_track.medium_id
     WHERE path_set.relative_path<>'' AND claim_medium.release_id=target.id AND claim_track.source_status='present'
    )
   )
  ) SELECT target_id::text,id::text,title FROM matches WHERE hint_rank<=$2 ORDER BY target_id,hint_rank`, ids, maxClaimHints)
	if err != nil {
		return nil, fmt.Errorf("%w: batch claim hints: %w", catalog.ErrQuery, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var hint ClaimHint
		if err := rows.Scan(&id, &hint.ID, &hint.Title); err != nil {
			return nil, err
		}
		hints[id] = append(hints[id], hint)
	}
	return hints, rows.Err()
}
