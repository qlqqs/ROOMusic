package catalog

import "strings"

// withArtistAliases 保留既有搜索的计数、稀疏/密集分页和深页语义；只扩展命中值来源。
// 姓名先按索引筛选，再沿有效署名传播，不把某轨的署名传播到兄弟曲目。
func (p searchPlan) withArtistAliases(query string) string {
	if !p.artistAliases || p.mode != "normalized" {
		return query
	}
	query = strings.ReplaceAll(query, "catalog_search_values", "search_values_with_artists")
	prefix := `matching_artist_names AS MATERIALIZED (
 SELECT n.id,n.artist_id,n.original_value,n.normalized_value,n.short_grams,n.loose_value,n.loose_grams
 FROM artist_search_names n
 WHERE ` + p.artistAliasPredicate() + `
 ), search_values_with_artists AS NOT MATERIALIZED (
 SELECT v.id,v.release_id,v.track_id,v.field,v.role,v.original_value,v.normalized_value,v.short_grams,v.loose_value,v.loose_grams,v.projection_generation,
 (SELECT min(credit.artist_id::text)::uuid FROM (
 SELECT e.artist_id FROM artist_effective_credits e JOIN artist_names cn ON cn.id=e.name_id
 WHERE e.track_id=v.track_id
 AND e.role=CASE WHEN v.field IN ('release_artist','album_artist','track_artist') THEN v.field ELSE v.role END
 AND cn.spelling=v.original_value
 UNION ALL
 SELECT e.artist_id FROM artist_effective_credits e JOIN artist_names cn ON cn.id=e.name_id
 WHERE e.release_id=v.release_id
 AND e.role=CASE WHEN v.field IN ('release_artist','album_artist','track_artist') THEN v.field ELSE v.role END
 AND cn.spelling=v.original_value
 ) credit HAVING count(DISTINCT credit.artist_id)=1) AS artist_id,
 NULL::text AS alias FROM catalog_search_values v
 UNION ALL
 SELECT n.id,e.release_id,e.track_id,'artist_alias',e.role,n.original_value,n.normalized_value,n.short_grams,n.loose_value,n.loose_grams,$7::bigint,n.artist_id,n.original_value
 FROM matching_artist_names n JOIN artist_effective_credits e ON e.artist_id=n.artist_id
 )`
	return strings.Replace(query, "WITH ", "WITH "+prefix+", ", 1)
}

// 别名传播共用艺人栏的完整三分支，仅把去标点参数换成目录查询中的位置。
func (p searchPlan) artistAliasPredicate() string {
	branches := artistSearchBranches(p.args[0].(string), p.args[7].(string), [2]bool{p.trigrams[0], p.trigrams[2]})
	return strings.NewReplacer("$4", "$8", "$5", "$9", "$6", "$10").Replace(artistNamePredicate(branches))
}
