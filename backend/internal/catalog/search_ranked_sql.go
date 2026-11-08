package catalog

import (
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

// normalizedPageSQL 按等级逐层取精确 top(offset+limit)，上层已足够时不展开较低等级。
// 每层先按全体匹配实体排序，绝无任意候选上限；低层只排除前面完整层已返回的实体。
// 说明的优先级不参与实体间排序，因此选页后才补原文不改变成员、顺序或match。
func (p searchPlan) normalizedPageSQL(attention bool) string {
	stages := make([]string, 0, 6)
	for rank := 0; rank < 6; rank++ {
		stages = append(stages, p.rankedStageSQL(rank, attention))
	}
	subjectKind := "release"
	if p.kind == "tracks" {
		subjectKind = "track"
	}
	query := `WITH title_values AS MATERIALIZED (
 SELECT subject_id,search_title FROM catalog_projection_subject_state
 WHERE subject_kind='` + subjectKind + `' AND search_applied_generation=$7
 ), ` + strings.Join(stages, ", ") + `, page AS MATERIALIZED (
  SELECT * FROM (SELECT * FROM tier0_page UNION ALL SELECT * FROM tier1_page UNION ALL SELECT * FROM tier2_page UNION ALL SELECT * FROM tier3_page UNION ALL SELECT * FROM tier4_page UNION ALL SELECT * FROM tier5_page) all_tiers
  ORDER BY match_rank,sort_title COLLATE "C",entity_id LIMIT $14 OFFSET $15
 ), page_sources AS MATERIALIZED (`

	if p.kind == "tracks" {
		query += `SELECT p.entity_id,p.match_rank,t.id AS track_id,m.release_id FROM page p JOIN tracks t ON t.id=p.entity_id
   JOIN media m ON m.id=t.medium_id`
	} else {
		relation := "releases.group_id=p.entity_id"
		if p.kind == "releases" {
			relation = "releases.id=p.entity_id"
		}
		query += `SELECT p.entity_id,p.match_rank,NULL::uuid AS track_id,releases.id AS release_id FROM page p
   JOIN releases ON ` + relation + ` WHERE ` + releasesql.ReleaseVisiblePredicate + `
   UNION ALL
   SELECT p.entity_id,p.match_rank,t.id,releases.id FROM page p JOIN releases ON ` + relation + `
   JOIN media m ON m.release_id=releases.id JOIN tracks t ON t.medium_id=m.id WHERE t.source_status='present'`
	}
	// 两个来源互斥，分别做参数化读取后合并。不能将 OR 下推到热门身份的全部署名：
	// 即使有 artist_id 索引，也会对每个页内来源重复过滤该身份的数万条署名。
	releaseGuard := ""
	if p.kind != "tracks" {
		releaseGuard = " AND ps.track_id IS NULL"
	}
	columns := "id,track_id,field,role,original_value,normalized_value,projection_generation,short_grams,loose_value,loose_grams"
	if p.artistAliases {
		columns += ",artist_id,alias"
	}
	descriptionValues := `SELECT ` + columns + ` FROM catalog_search_values WHERE track_id=ps.track_id
 UNION ALL
 SELECT ` + columns + ` FROM catalog_search_values WHERE release_id=ps.release_id` + releaseGuard
	// 每个已选页的来源先取最佳说明，再对实体取最优；绝不限制匹配候选或可翻页结果。
	query += `), descriptions AS (
  SELECT DISTINCT ON (ps.entity_id) ps.entity_id,ps.release_id AS matched_release_id,h.*
  FROM page_sources ps JOIN LATERAL (
   SELECT v.track_id,v.field,v.role,v.original_value,v.id AS value_id,` + normalizedSearchRank + ` AS match_rank
   FROM (` + descriptionValues + ` OFFSET 0) v WHERE ` + p.matchPredicate + ` AND (` + normalizedSearchRank + `)=ps.match_rank
   ORDER BY match_rank,` + p.fieldPriority() + `,` + searchRoleOrder + `,v.original_value COLLATE "C",v.track_id,v.id LIMIT 1
  ) h ON TRUE
  ORDER BY ps.entity_id,h.match_rank,` + p.fieldPriority() + `,` + searchRoleOrder + `,
   h.original_value COLLATE "C",ps.release_id,h.track_id,h.value_id
 )
 SELECT p.entity_id::text,d.matched_release_id::text,d.track_id::text,d.field,
 CASE WHEN d.field IN ('release_artist','album_artist','track_artist') THEN d.field ELSE d.role END,
 d.original_value,d.match_rank FROM page p JOIN descriptions d ON d.entity_id=p.entity_id
 ORDER BY p.match_rank,p.sort_title COLLATE "C",p.entity_id`
	return query
}
