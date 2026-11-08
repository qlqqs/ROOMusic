package catalog

import (
	"fmt"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

// normalizedTierMatch 的哈希只是有界索引定位，仍比较完整键；长标签不放进B-tree键。
func (p searchPlan) normalizedTierMatch(rank int) string {
	return p.normalizedMatch(rank)
}

func (p searchPlan) rankedStageSQL(rank int, attention bool) string {
	name := fmt.Sprintf("tier%d", rank)
	priorPages := make([]string, 0, rank)
	for previous := 0; previous < rank; previous++ {
		priorPages = append(priorPages, fmt.Sprintf("SELECT entity_id FROM tier%d_page", previous))
	}
	// 合法OFFSET也可能接近bigint上限；top-k额外取一行不能让极大页码变成数据库错误。
	// 饱和的是整型表示范围，不是业务候选上限；实际页仍使用调用方完整OFFSET。
	remaining, exclude := `(CASE WHEN $15::bigint>9223372036854775807-$14::bigint
 THEN 9223372036854775807 ELSE $14::bigint+$15::bigint END)`, "TRUE"
	if rank > 0 {
		prior := strings.Join(priorPages, " UNION ALL ")
		remaining += `-(SELECT count(*) FROM (` + prior + `) preceding)`
		exclude = `NOT EXISTS (SELECT 1 FROM (` + prior + `) preceding WHERE preceding.entity_id=b.entity_id)`
	}
	// 使用常量子计划作为整层开关；不是截断subject候选。低等级无需求时底层节点actual loops=0。
	query := name + `_probe AS MATERIALIZED (` + rawHitProbeSQL(p.normalizedTierMatch(rank)) + `), ` + name + `_subjects AS MATERIALIZED (
 SELECT v.release_id,v.track_id FROM catalog_search_values v
 WHERE (` + remaining + `)>0 AND (SELECT n FROM ` + name + `_probe)<=4096 AND ` + p.normalizedTierMatch(rank) + ` GROUP BY v.release_id,v.track_id
 ), ` + name + `_entities AS MATERIALIZED (`
	if p.kind == "tracks" {
		query += `SELECT t.id AS entity_id FROM ` + name + `_subjects h JOIN tracks t ON t.id=h.track_id
 WHERE t.source_status='present' AND h.track_id IS NOT NULL
 UNION ALL
 SELECT t.id FROM ` + name + `_subjects h JOIN media m ON m.release_id=h.release_id
 JOIN tracks t ON t.medium_id=m.id WHERE t.source_status='present' AND h.release_id IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM ` + name + `_subjects direct WHERE direct.track_id=t.id)`
	} else {
		entity := "releases.group_id"
		if p.kind == "releases" {
			entity = "releases.id"
		}
		query += `SELECT ` + entity + ` AS entity_id FROM ` + name + `_subjects h
 JOIN releases ON releases.id=h.release_id WHERE h.release_id IS NOT NULL AND ` + releasesql.ReleaseVisiblePredicate + `
 UNION
 SELECT ` + entity + ` FROM ` + name + `_subjects h JOIN tracks t ON t.id=h.track_id
 JOIN media m ON m.id=t.medium_id JOIN releases ON releases.id=m.release_id
 WHERE t.source_status='present' AND h.track_id IS NOT NULL`
	}
	query += `)`
	if p.kind == "albums" {
		query += `, ` + name + `_representatives AS MATERIALIZED (
 SELECT DISTINCT ON (releases.group_id) releases.group_id AS entity_id,releases.id
 FROM ` + releasesql.ReleaseSource + ` WHERE ` + releasesql.ReleaseVisiblePredicate + `
 AND releases.group_id IN (SELECT entity_id FROM ` + name + `_entities)
 ORDER BY releases.group_id,` + releasesql.ReleaseGroupMemberOrder + `)`
	}
	titleSubject := "b.entity_id"
	base := name + `_entities`
	if p.kind == "albums" {
		// representatives已按命中组过滤且每组唯一，不再次连接entities导致重复扫描。
		base = name + `_representatives`
		titleSubject = "b.id"
	}
	query += `, ` + name + `_sparse_page AS MATERIALIZED (
 SELECT b.entity_id,` + fmt.Sprint(rank) + ` AS match_rank,COALESCE(tv.search_title,'') AS sort_title
 FROM ` + base + ` b `
	query += `LEFT JOIN title_values tv ON tv.subject_id=` + titleSubject + ` WHERE (SELECT n FROM ` + name + `_probe) BETWEEN 1 AND 4096 AND (` + remaining + `)>0 AND ` + exclude
	if attention {
		condition := `releases.group_id=b.entity_id`
		if p.kind == "releases" {
			condition = `releases.id=b.entity_id`
		}
		query += ` AND EXISTS (SELECT 1 FROM ` + releasesql.ReleaseSource + ` WHERE ` + condition + ` AND ` +
			releasesql.ReleaseVisiblePredicate + ` AND ` + releasesql.ReleaseAttentionExpression + `>0)`
	}
	query += ` ORDER BY COALESCE(tv.search_title,'') COLLATE "C",b.entity_id LIMIT (` + remaining + `))`
	broadGuard := exclude + ` AND (` + remaining + `)>0 AND (SELECT n FROM ` + name + `_probe)>4096`
	return query + `, ` + name + `_page AS MATERIALIZED (
 SELECT * FROM ` + name + `_sparse_page UNION ALL SELECT * FROM (` + p.orderedTierSQL(rank, remaining, broadGuard, attention) + `) broad_page)`
}
