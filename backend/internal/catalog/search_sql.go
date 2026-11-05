package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/releasesql"
	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

// searchPlan 同时产生页和计数的完整命中关系；没有候选数量上限。
type searchPlan struct {
	kind, mode, hits string
	matchPredicate   string
	args             []any
	empty            bool
	artistAliases    bool
}

func newSearchPlan(query, kind, mode string, generation int64) searchPlan {
	plan := searchPlan{kind: kind, mode: mode, empty: query == "", matchPredicate: normalizedSearchMatch}
	if mode == "normalized" {
		key, number := textnorm.Text(query), textnorm.CatalogNumber(query)
		plan.empty = key == ""
		plan.args = []any{key, searchindex.EscapeLike(key), searchindex.QueryGram(key),
			number, searchindex.EscapeLike(number), searchindex.QueryGram(number), generation}
		plan.hits = `hits AS NOT MATERIALIZED (
		 SELECT v.id AS value_id,v.release_id,v.track_id,v.field,v.role,v.original_value,
		 ` + normalizedSearchRank + ` AS match_rank
		 FROM catalog_search_values v WHERE ` + normalizedSearchMatch + `)`
	} else {
		plan.args = []any{escapeLikePattern(query)}
		trackFields := `('track_title',t.title)`
		if kind == "tracks" {
			trackFields += `,('track_artist',t.artist)`
		}
		plan.hits = `hits AS NOT MATERIALIZED (
		 SELECT releases.id AS value_id,releases.id AS release_id,NULL::uuid AS track_id,f.field,NULL::text AS role,f.original_value,
		 CASE WHEN f.original_value ILIKE $1 ESCAPE '~' THEN 0 WHEN f.original_value ILIKE $1 || '%' ESCAPE '~' THEN 1 ELSE 2 END AS match_rank
		 FROM ` + releasesql.ReleaseSource + ` CROSS JOIN LATERAL (VALUES
		 ('release_title',releases.title),('release_artist',releases.artist),('album_artist',releases.album_artist)) f(field,original_value)
		 WHERE f.original_value ILIKE '%' || $1 || '%' ESCAPE '~'
		 UNION ALL
		 SELECT t.id,NULL::uuid,t.id,f.field,NULL::text,f.original_value,
		 CASE WHEN f.original_value ILIKE $1 ESCAPE '~' THEN 0 WHEN f.original_value ILIKE $1 || '%' ESCAPE '~' THEN 1 ELSE 2 END
		 FROM tracks t CROSS JOIN LATERAL (VALUES ` + trackFields + `) f(field,original_value)
		 WHERE t.source_status='present' AND f.original_value ILIKE '%' || $1 || '%' ESCAPE '~'
		)`
	}
	return plan
}

func (p searchPlan) countSQL(bounded bool, attention bool) string {
	if p.mode == "normalized" {
		return p.withArtistAliases(p.normalizedCountSQL(bounded, attention))
	}
	prefix, subject := p.hits, "hits"
	var candidates string
	if p.kind == "tracks" {
		candidates = `SELECT t.id FROM tracks t JOIN media m ON m.id=t.medium_id
		 WHERE t.source_status='present' AND
		 (EXISTS (SELECT 1 FROM ` + subject + ` h WHERE h.track_id IS NOT NULL AND h.track_id=t.id)
		 OR EXISTS (SELECT 1 FROM ` + subject + ` h WHERE h.release_id IS NOT NULL AND h.release_id=m.release_id))`
	} else {
		// 这里只传播原始命中，不 UNION 去重/物化全部发行。计数从唯一实体基表早停。
		prefix += `, matching_releases AS NOT MATERIALIZED (
         SELECT releases.id AS release_id FROM releases WHERE
         ((EXISTS (SELECT 1 FROM ` + subject + ` h WHERE h.release_id IS NOT NULL AND h.release_id=releases.id)
           AND ` + releasesql.ReleaseVisiblePredicate + `)
          OR EXISTS (SELECT 1 FROM tracks st JOIN media sm ON sm.id=st.medium_id
           WHERE sm.release_id=releases.id AND st.source_status='present'
           AND EXISTS (SELECT 1 FROM ` + subject + ` h WHERE h.track_id IS NOT NULL AND h.track_id=st.id)))
        )`
		if p.kind == "releases" {
			candidates = `SELECT releases.id FROM ` + releasesql.ReleaseSource + ` WHERE ` + releasesql.ReleaseVisiblePredicate + `
			 AND (EXISTS (SELECT 1 FROM matching_releases mr WHERE mr.release_id=releases.id)) IS TRUE`
			if attention {
				candidates += ` AND ` + releasesql.ReleaseAttentionExpression + `>0`
			}
		} else {
			candidates = `SELECT g.id FROM release_groups g WHERE (EXISTS (
			 SELECT 1 FROM matching_releases mr JOIN ` + releasesql.ReleaseSource + ` ON releases.id=mr.release_id
			 WHERE releases.group_id=g.id AND ` + releasesql.ReleaseVisiblePredicate + `)) IS TRUE`
			if attention {
				candidates += ` AND EXISTS (SELECT 1 FROM ` + releasesql.ReleaseSource + ` WHERE releases.group_id=g.id AND ` +
					releasesql.ReleaseVisiblePredicate + ` AND ` + releasesql.ReleaseAttentionExpression + `>0)`
			}
		}
	}
	if bounded {
		candidates += ` LIMIT 1001`
	}

	return `WITH ` + prefix + ` SELECT COUNT(*) FROM (` + candidates + `) counted`
}

func (s *Store) countSearch(ctx context.Context, plan searchPlan, bounded bool) (int64, error) {
	var total int64
	err := s.database.QueryRowContext(ctx, plan.countSQL(bounded, false), plan.args...).Scan(&total)
	return total, err
}

// 先传播命中到当前归属，再按实体选一条说明；曲目级命中不会传播到兄弟曲目。
func (p searchPlan) entityHitsSQL() string {
	if p.kind == "tracks" {
		return `entity_hits AS (
		 SELECT t.id AS entity_id,m.release_id AS matched_release_id,h.track_id,h.field,h.role,h.original_value,h.match_rank,h.value_id
		 FROM hits h JOIN tracks t ON t.id=h.track_id JOIN media m ON m.id=t.medium_id WHERE t.source_status='present'
		 UNION ALL
		 SELECT t.id,m.release_id,h.track_id,h.field,h.role,h.original_value,h.match_rank,h.value_id
		 FROM hits h JOIN media m ON m.release_id=h.release_id JOIN tracks t ON t.medium_id=m.id WHERE t.source_status='present'
		)`
	}
	entity := `releases.group_id`
	if p.kind == "releases" {
		entity = `releases.id`
	}
	return `entity_hits AS (
	 SELECT ` + entity + ` AS entity_id,releases.id AS matched_release_id,h.track_id,h.field,h.role,h.original_value,h.match_rank,h.value_id
	 FROM hits h JOIN ` + releasesql.ReleaseSource + ` ON releases.id=h.release_id WHERE ` + releasesql.ReleaseVisiblePredicate + `
	 UNION ALL
	 SELECT ` + entity + `,releases.id,h.track_id,h.field,h.role,h.original_value,h.match_rank,h.value_id
	 FROM hits h JOIN tracks t ON t.id=h.track_id JOIN media m ON m.id=t.medium_id
	 JOIN ` + releasesql.ReleaseSource + ` ON releases.id=m.release_id WHERE t.source_status='present'
	)`
}

func (p searchPlan) fieldPriority() string {
	own, inherited := "release_title", "track_title"
	if p.kind == "tracks" {
		own, inherited = inherited, own
	}
	return `CASE field WHEN '` + own + `' THEN 0 WHEN '` + inherited + `' THEN 1
	 WHEN 'release_artist' THEN 2 WHEN 'album_artist' THEN 3 WHEN 'track_artist' THEN 4
	 WHEN 'credit' THEN 5 WHEN 'label' THEN 6 WHEN 'catalog_number' THEN 7 ELSE 8 END`
}

const searchRoleOrder = `CASE role WHEN 'composer' THEN 0 WHEN 'conductor' THEN 1 WHEN 'performer' THEN 2 WHEN 'producer' THEN 3 ELSE 4 END`

func (p searchPlan) pageSQL(attention bool) string {
	if p.mode == "normalized" {
		query := p.normalizedPageSQL(attention)
		if p.artistAliases {
			query = strings.Replace(query, "v.id AS value_id,", "v.id AS value_id,v.artist_id,v.alias,", 1)
			query = strings.Replace(query, "d.original_value,d.match_rank FROM", "d.original_value,d.match_rank,d.artist_id::text,d.alias FROM", 1)
		}
		return p.withArtistAliases(query)
	}
	query := `WITH ` + p.hits + `, ` + p.entityHitsSQL() + `, best AS MATERIALIZED (
	 SELECT DISTINCT ON (entity_id) entity_id,matched_release_id,track_id,field,role,original_value,match_rank,value_id
	 FROM entity_hits ORDER BY entity_id,match_rank,` + p.fieldPriority() + `,` + searchRoleOrder + `,
	 original_value COLLATE "C",matched_release_id,track_id,value_id)`
	if p.kind == "albums" {
		// 代表版本仅为 best 中实际命中的组各算一次；不对每个组重新扫描发行，也不排名无关组。
		query += `, representatives AS (
		 SELECT DISTINCT ON (releases.group_id) releases.group_id,releases.id,releases.title,releases.artist
		 FROM ` + releasesql.ReleaseSource + `
		 WHERE ` + releasesql.ReleaseVisiblePredicate + `
		 AND releases.group_id IN (SELECT entity_id FROM best)
		 ORDER BY releases.group_id,` + releasesql.ReleaseGroupMemberOrder + `
		)`
	}
	query += ` SELECT b.entity_id::text,b.matched_release_id::text,b.track_id::text,b.field,
	 CASE WHEN b.field IN ('release_artist','album_artist','track_artist') THEN b.field ELSE b.role END,b.original_value,b.match_rank
	 FROM best b `
	var titleSubject, titleField, legacyOrder string
	switch p.kind {
	case "tracks":
		query += `JOIN tracks t ON t.id=b.entity_id `
		titleSubject, titleField = `title_value.track_id=t.id`, "track_title"
		legacyOrder = `LOWER(t.title),LOWER(t.artist),t.id`
	case "releases":
		query += `JOIN ` + releasesql.ReleaseSource + ` ON releases.id=b.entity_id `
		titleSubject, titleField = `title_value.release_id=releases.id`, "release_title"
		legacyOrder = `LOWER(releases.title),LOWER(releases.artist),releases.id`
	default:
		// 仅选代表行作为排序键；命中的成员从不替换代表，也不先分页成员再去重组。
		query += `JOIN representatives representative ON representative.group_id=b.entity_id `
		titleSubject, titleField = `title_value.release_id=representative.id`, "release_title"
		legacyOrder = `LOWER(representative.title),LOWER(representative.artist),representative.id`
	}
	order := legacyOrder
	if p.mode == "normalized" {
		query += `LEFT JOIN catalog_search_values title_value ON ` + titleSubject +
			` AND title_value.field='` + titleField + `' AND title_value.position=1 AND title_value.projection_generation=$7 `
		order = `b.match_rank,COALESCE(title_value.normalized_value,'') COLLATE "C",b.entity_id`
	}
	if attention {
		if p.kind == "releases" {
			query += `WHERE ` + releasesql.ReleaseAttentionExpression + `>0 `
		} else {
			query += `WHERE EXISTS (SELECT 1 FROM ` + releasesql.ReleaseSource + ` WHERE releases.group_id=b.entity_id AND ` +
				releasesql.ReleaseVisiblePredicate + ` AND ` + releasesql.ReleaseAttentionExpression + `>0) `
		}
	}
	return query + `ORDER BY ` + order + ` LIMIT $` + strconv.Itoa(len(p.args)+1) + ` OFFSET $` + strconv.Itoa(len(p.args)+2)
}

type searchHit struct {
	ID, MatchedReleaseID string
	Match                SearchMatch
}

func (s *Store) searchPage(ctx context.Context, plan searchPlan, limit, offset int) ([]searchHit, bool, error) {
	return s.searchPageFiltered(ctx, plan, limit, offset, false)
}

func (s *Store) searchPageFiltered(ctx context.Context, plan searchPlan, limit, offset int, attention bool) ([]searchHit, bool, error) {
	args := append(append([]any{}, plan.args...), limit+1, offset)
	rows, err := s.database.QueryContext(ctx, plan.pageSQL(attention), args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	hits := make([]searchHit, 0, limit)
	hasMore := false
	for rows.Next() {
		if len(hits) == limit {
			hasMore = true
			break
		}
		var hit searchHit
		var matchedRelease, trackID sql.NullString
		var rank int
		targets := []any{&hit.ID, &matchedRelease, &trackID, &hit.Match.Field, &hit.Match.Role, &hit.Match.Original, &rank}
		if plan.artistAliases && plan.mode == "normalized" {
			targets = append(targets, &hit.Match.ArtistID, &hit.Match.Alias)
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, false, err
		}
		if matchedRelease.Valid {
			hit.MatchedReleaseID = matchedRelease.String
		}
		if trackID.Valid {
			hit.Match.TrackID = &trackID.String
		}
		if rank < 0 || rank > 2 || len(hit.Match.Original) > 4096 {
			return nil, false, fmt.Errorf("invalid search match projection")
		}
		hit.Match.Kind = [...]string{"exact", "prefix", "contains"}[rank]
		hit.Match.ReleaseID = &hit.MatchedReleaseID
		hits = append(hits, hit)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	return hits, hasMore, nil
}

// prepareSearchPlan 让PostgreSQL按实际locale判断连续字母/数字，而不是在Go里猜rune数。
// 这是可提取trigram的保守充分条件；其余输入只走short-gram并完整LIKE复核。
func (s *Store) prepareSearchPlan(ctx context.Context, plan searchPlan) (searchPlan, error) {
	if plan.mode != "normalized" || plan.empty {
		return plan, nil
	}
	if plan.artistAliases {
		if err := s.database.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artist_search_names n
   WHERE n.projection_generation=(SELECT published_generation FROM catalog_projection_state WHERE capability='artists')
   AND n.short_grams @> ARRAY[$1]::text[] AND (n.normalized_value||'') LIKE '%'||$2||'%' ESCAPE '~')`, plan.args[2], plan.args[1]).Scan(&plan.artistAliases); err != nil {
			return plan, err
		}
	}
	var textTrigram, numberTrigram bool
	if err := s.database.QueryRowContext(ctx, `SELECT $1::text ~ '[[:alnum:]]{3}',$2::text ~ '[[:alnum:]]{3}'`, plan.args[0], plan.args[3]).Scan(&textTrigram, &numberTrigram); err != nil {
		return plan, err
	}
	match := plan.matchPredicate
	if plan.args[0] == plan.args[3] {
		// 两种规范键相同，不重复扫描同一个GIN分支；仍显式绑定所有参数类型。
		match = `$1::text<>'' AND $4::text IS NOT NULL AND $5::text IS NOT NULL AND $6::text IS NOT NULL
 AND v.projection_generation=$7 AND v.short_grams @> ARRAY[$3]::text[]
 AND v.normalized_value LIKE '%' || $2 || '%' ESCAPE '~'`
	}
	if !textTrigram {
		match = strings.ReplaceAll(match, `v.normalized_value LIKE '%' || $2`, `(v.normalized_value || '') LIKE '%' || $2`)
	}
	if !numberTrigram {
		match = strings.ReplaceAll(match, `v.normalized_value LIKE '%' || $5`, `(v.normalized_value || '') LIKE '%' || $5`)
	}
	plan.hits = strings.ReplaceAll(plan.hits, plan.matchPredicate, match)
	plan.matchPredicate = match
	return plan, nil
}
