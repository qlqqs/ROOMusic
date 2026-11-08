package catalog

import (
	"fmt"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
)

// rawHitProbeLimit 只选择执行策略，不限制任何结果候选、计数或可翻页集合。
// 稀疏命中从GIN出发；密集命中按完整标题序探测唯一实体，避免热门词全量扇出。
const rawHitProbeLimit = 4096

func rawHitProbeSQL(predicate string) string {
	return `SELECT count(*) AS n FROM (SELECT 1 FROM catalog_search_values v WHERE ` + predicate + ` LIMIT 4097) raw_probe`
}

// subjectMatchSQL 先按来源主键读该对象的完整字段，再检查搜索谓词。
// OFFSET 0 保留这一关联边界，避免通用计划为每个对象重扫热门词的全局GIN命中。
func subjectMatchSQL(relation, predicate string) string {
	return `EXISTS (SELECT 1 FROM (SELECT field,normalized_value,projection_generation,short_grams,loose_value,loose_grams FROM catalog_search_values WHERE ` + relation + ` OFFSET 0) v
 WHERE ` + predicate + ` OFFSET 0)`
}

func (p searchPlan) normalizedEntityMatchSQL(entity, release, predicate string) string {
	if p.kind == "tracks" {
		return `(` + subjectMatchSQL("track_id="+entity, predicate) + ` OR ` + subjectMatchSQL("release_id="+release, predicate) + `)`
	}
	link := "r.group_id=" + entity
	if p.kind == "releases" {
		link = "r.id=" + entity
	}
	return `EXISTS (SELECT 1 FROM releases r WHERE ` + link + ` AND (
 (` + subjectMatchSQL("release_id=r.id", predicate) + `
  AND EXISTS (SELECT 1 FROM media m JOIN tracks t ON t.medium_id=m.id WHERE m.release_id=r.id AND t.source_status='present'))
 OR EXISTS (SELECT 1 FROM media m JOIN tracks t ON t.medium_id=m.id WHERE m.release_id=r.id AND t.source_status='present'
  AND ` + subjectMatchSQL("track_id=t.id", predicate) + `)
 ) OFFSET 0)`
}

// 搜索代表选择只需知道每个发行是否至少有一首在场曲目，不先聚合全库曲目。
// OFFSET 0 保持按发行关联的 EXISTS；语义与 releasesql.ReleaseVisiblePredicate 相同。
const searchReleaseVisiblePredicate = `EXISTS (
 SELECT 1 FROM media visible_media JOIN tracks visible_tracks ON visible_tracks.medium_id=visible_media.id
 WHERE visible_media.release_id=releases.id AND visible_tracks.source_status='present' OFFSET 0
)`

// orderedEntitiesSQL 的前缀键只支持增量排序，后面仍比较完整标题，超过128 scalar不会被截词或误排。
func (p searchPlan) orderedEntitiesSQL() string {
	if p.kind == "tracks" {
		return `SELECT s.subject_id AS entity_id,s.search_title AS sort_title,left(s.search_title,128) COLLATE "C" AS sort_prefix
 FROM catalog_projection_subject_state s
 WHERE s.subject_kind='track' AND s.search_applied_generation=$7
 ORDER BY sort_prefix,s.search_title COLLATE "C",s.subject_id OFFSET 0`
	}
	if p.kind == "releases" {
		return `SELECT s.subject_id AS entity_id,s.search_title AS sort_title,left(s.search_title,128) COLLATE "C" AS sort_prefix FROM catalog_projection_subject_state s
 WHERE s.subject_kind='release' AND s.search_applied_generation=$7
 ORDER BY sort_prefix,s.search_title COLLATE "C",s.subject_id OFFSET 0`
	}
	// 先沿发行标题索引有序探测，只保留各组当前代表；不用为热门词先聚合全库在场曲目。
	// 同标题最后仍按group ID排序，不能沿用发行ID；OFFSET 0保留有序候选流，LIMIT在完整匹配之后。
	return `SELECT placement.group_id AS entity_id,s.search_title AS sort_title,s.sort_prefix FROM (
 SELECT subject_id,search_title,left(search_title,128) COLLATE "C" AS sort_prefix
 FROM catalog_projection_subject_state WHERE subject_kind='release' AND search_applied_generation=$7
 ORDER BY sort_prefix,search_title COLLATE "C",subject_id OFFSET 0
 ) s JOIN LATERAL (
 SELECT candidate.group_id FROM releases candidate WHERE candidate.id=s.subject_id AND candidate.id=(
  SELECT releases.id FROM ` + releasesql.ReleaseSource + `
  WHERE releases.group_id=candidate.group_id AND ` + searchReleaseVisiblePredicate + `
  ORDER BY ` + releasesql.ReleaseGroupMemberOrder + ` LIMIT 1
 ) OFFSET 0
 ) placement ON TRUE
 ORDER BY s.sort_prefix,s.search_title COLLATE "C",placement.group_id OFFSET 0`
}

func (p searchPlan) attentionSQL(entity string) string {
	link := "releases.group_id=" + entity
	if p.kind == "releases" {
		link = "releases.id=" + entity
	}
	return `EXISTS (SELECT 1 FROM ` + releasesql.ReleaseSource + ` WHERE ` + link + ` AND ` + releasesql.ReleaseVisiblePredicate + ` AND ` + releasesql.ReleaseAttentionExpression + `>0)`
}

func (p searchPlan) orderedTierSQL(rank int, remaining, exclude string, attention bool) string {
	release := ""
	if p.kind == "tracks" {
		release = "placement.release_id"
	}
	predicate := p.matchPredicate + fmt.Sprintf(" AND (%s)=%d", normalizedSearchRank, rank)
	query := `SELECT b.entity_id,` + fmt.Sprint(rank) + ` AS match_rank,b.sort_title FROM (` + p.orderedEntitiesSQL() + `) b `
	if p.kind == "tracks" {
		query += `JOIN LATERAL (SELECT m.release_id FROM tracks t JOIN media m ON m.id=t.medium_id
 WHERE t.id=b.entity_id AND t.source_status='present' OFFSET 0) placement ON TRUE `
	}
	query += `WHERE ` + exclude + ` AND ` + p.normalizedEntityMatchSQL("b.entity_id", release, predicate)
	if attention {
		query += ` AND ` + p.attentionSQL("b.entity_id")
	}
	// 子查询已按完整标题/ID有序；保留同序LIMIT，外层仅对这一页重排成统一DTO顺序。
	order := `b.sort_prefix,b.sort_title COLLATE "C",b.entity_id`
	return query + ` ORDER BY ` + order + ` LIMIT (` + remaining + `)`
}

// denseCountCandidatesSQL 从唯一基表逐个做完整匹配；LIMIT1001前没有实体聚合或候选截取。
func (p searchPlan) denseCountCandidatesSQL(attention bool) string {
	entity, release, base := "g.id", "", "release_groups g"
	condition := ""
	if p.kind == "tracks" {
		entity, release, base = "t.id", "m.release_id", "tracks t JOIN media m ON m.id=t.medium_id"
		condition = " AND t.source_status='present'"
	} else if p.kind == "releases" {
		entity, base = "r0.id", "releases r0"
	}
	query := `SELECT ` + entity + ` FROM ` + base + ` WHERE (SELECT n FROM count_probe)>4096` + condition +
		` AND ` + p.normalizedEntityMatchSQL(entity, release, p.matchPredicate)
	if attention {
		query += ` AND ` + p.attentionSQL(entity)
	}
	return query
}

// normalizedCountSQL 的有界计数仅在探测确认稀疏时物化完整原始命中（不超过4096行），
// 不物化或聚合传播后的实体。计数仍从唯一实体基表做半连接，LIMIT1001先于COUNT。
// 旧列表的精确计数没有早停机会，直接使用全部原始命中，避免逐实体关联全库。
// 显式的原始命中边界防止新增关系索引后planner反向逐曲目扫描值表。
func (p searchPlan) normalizedCountSQL(bounded, attention bool) string {
	prefix := `WITH count_probe AS MATERIALIZED (` + rawHitProbeSQL(p.matchPredicate) + `),
 hit_subjects AS MATERIALIZED (
 SELECT v.release_id,v.track_id FROM catalog_search_values v
 WHERE (SELECT n FROM count_probe)<=4096 AND ` + p.matchPredicate + `)`
	if !bounded {
		// 旧列表必须精确计数，无早停收益；从完整索引命中传播，而非逐个未命中实体
		// 重复检查它的全部曲目。这里只物化原始来源，仍按唯一实体基表计数。
		prefix = `WITH hit_subjects AS MATERIALIZED (
 SELECT v.release_id,v.track_id FROM catalog_search_values v WHERE ` + p.matchPredicate + `)`
	}
	var candidates string
	if p.kind == "tracks" {
		candidates = `SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id
 WHERE t.source_status='present' AND (EXISTS (SELECT 1 FROM hit_subjects h WHERE h.track_id=t.id)
 OR EXISTS (SELECT 1 FROM hit_subjects h WHERE h.release_id=m.release_id))`
	} else {
		prefix += `, matching_releases AS NOT MATERIALIZED (
 SELECT h.release_id FROM hit_subjects h JOIN releases ON releases.id=h.release_id
 WHERE h.release_id IS NOT NULL AND ` + releasesql.ReleaseVisiblePredicate + `
 UNION ALL
 SELECT m.release_id FROM hit_subjects h JOIN tracks t ON t.id=h.track_id JOIN media m ON m.id=t.medium_id
 WHERE h.track_id IS NOT NULL AND t.source_status='present')`
		if p.kind == "albums" {
			candidates = `SELECT g.id FROM release_groups g WHERE (g.id IN (
 SELECT r.group_id FROM matching_releases mr JOIN releases r ON r.id=mr.release_id)) IS TRUE`
		} else {
			candidates = `SELECT r.id FROM releases r WHERE (r.id IN (SELECT release_id FROM matching_releases)) IS TRUE`
		}
		if attention {
			entity := "g.id"
			if p.kind == "releases" {
				entity = "r.id"
			}
			candidates += ` AND ` + p.attentionSQL(entity)
		}
	}
	if !bounded {
		return prefix + ` SELECT COUNT(*) FROM (` + candidates + `) counted`
	}
	broad := p.denseCountCandidatesSQL(attention)
	candidates += ` LIMIT 1001`
	broad += ` LIMIT 1001`
	return prefix + ` SELECT COUNT(*) FROM (SELECT * FROM (` + candidates + `) sparse WHERE (SELECT n FROM count_probe)<=4096
 UNION ALL SELECT * FROM (` + broad + `) dense) counted`
}
