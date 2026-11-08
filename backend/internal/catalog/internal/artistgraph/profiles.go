package artistgraph

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/qlqq/roomusic/backend/internal/catalog/internal/searchindex"
	"github.com/qlqq/roomusic/backend/internal/catalog/textnorm"
)

// 名字归属既含默认/人工全局绑定，也含作用域署名；可同时属于两个拆开的同名人。
// 先从本批 canonical 身份找一跳路由；有效署名按 (artist_id,name_id) 索引逐个不同名字前进，
// 不为枚举少数别名重复展开热门身份的全部轨目，也不截断名字集合。
const ownedNames = `WITH RECURSIVE routes AS (
 SELECT a.id AS artist_id,a.id AS route_id FROM artists a
 LEFT JOIN artist_overrides o ON o.artist_id=a.id
 WHERE a.id=ANY($1::uuid[]) AND o.redirect_artist_id IS NULL
 UNION ALL SELECT o.redirect_artist_id,o.artist_id FROM artist_overrides o WHERE o.redirect_artist_id=ANY($1::uuid[])
 ), credit_names AS (
 SELECT a.id AS artist_id,n.name_id FROM unnest($1::uuid[]) a(id)
 JOIN LATERAL(SELECT name_id FROM artist_effective_credits WHERE artist_id=a.id ORDER BY name_id LIMIT 1) n ON TRUE
 UNION ALL SELECT c.artist_id,n.name_id FROM credit_names c
 JOIN LATERAL(SELECT name_id FROM artist_effective_credits WHERE artist_id=c.artist_id AND name_id>c.name_id ORDER BY name_id LIMIT 1) n ON TRUE
 )
 SELECT r.artist_id,n.id AS name_id FROM routes r
 JOIN LATERAL (
 SELECT k.id FROM artist_name_keys k WHERE k.initial_artist_id=r.route_id
 AND NOT EXISTS(SELECT 1 FROM artist_name_bindings b WHERE b.key_id=k.id)
 UNION ALL SELECT b.key_id FROM artist_name_bindings b WHERE b.artist_id=r.route_id
 ) k ON TRUE JOIN artist_names n ON n.key_id=k.id
 UNION SELECT artist_id,name_id FROM credit_names`

func refreshProfiles(ctx context.Context, tx *sql.Tx, ids []string, gen int64) error {
	if len(ids) == 0 {
		return nil
	}
	// 4097行探测只选择稀疏/热门读取路径，不限制后续计票或名字集合。
	// 稀疏批不扫描全 missing 集合；热门批避免 generic plan 对每条署名查 tracks。
	var dense bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM artist_effective_credits
 WHERE artist_id=ANY($1::uuid[]) AND track_id IS NOT NULL OFFSET 4096 LIMIT 1)`, ids).Scan(&dense); err != nil {
		return err
	}
	absentCTE, absentSource, absentFilter := "", "tracks", " AND t.source_status<>'present'"
	allTrackCounts := `all_track_counts AS (
 SELECT artist_id,name_id,count(DISTINCT track_id) AS votes FROM artist_effective_credits
 WHERE artist_id=ANY($1::uuid[]) AND track_id IS NOT NULL GROUP BY artist_id,name_id
 )`
	if dense {
		absentCTE = "absent_tracks AS MATERIALIZED (SELECT id FROM tracks WHERE source_status<>'present'), "
		absentSource, absentFilter = "absent_tracks", ""
		// 按身份完整聚合，避免低可见性时将热门批全部署名合并成一次外部排序。
		// 不限制单身份轨数；跨角色仍按同name/track去重，入口按uuid类型去重。
		allTrackCounts = `all_track_counts AS (
 SELECT a.id AS artist_id,c.name_id,c.votes FROM (SELECT DISTINCT id FROM unnest($1::uuid[]) ids(id)) a
 CROSS JOIN LATERAL (
 SELECT name_id,count(DISTINCT track_id) AS votes FROM artist_effective_credits
 WHERE artist_id=a.id AND track_id IS NOT NULL GROUP BY name_id ORDER BY name_id
 ) c
 )`
	}
	// 每 subject/name 跨角色一票；全体去重票减去非 present 去重票。
	rows, err := tx.QueryContext(ctx, `WITH `+absentCTE+allTrackCounts+`, absent_track_counts AS (
 SELECT c.artist_id,c.name_id,count(DISTINCT c.track_id) AS votes FROM artist_effective_credits c
 JOIN `+absentSource+` t ON t.id=c.track_id WHERE c.artist_id=ANY($1::uuid[])`+absentFilter+`
 GROUP BY c.artist_id,c.name_id
 ), track_counts AS (
 SELECT a.artist_id,a.name_id,a.votes-COALESCE(m.votes,0) AS votes FROM all_track_counts a
 LEFT JOIN absent_track_counts m ON m.artist_id=a.artist_id AND m.name_id=a.name_id
 WHERE a.votes>COALESCE(m.votes,0)
 ), release_votes AS MATERIALIZED (
 SELECT DISTINCT artist_id,release_id,name_id FROM artist_effective_credits
 WHERE artist_id=ANY($1::uuid[]) AND release_id IS NOT NULL
 ), release_counts AS (
 SELECT c.artist_id,c.name_id,count(*) AS votes FROM release_votes c
 JOIN LATERAL(SELECT 1 FROM media m JOIN tracks t ON t.medium_id=m.id
 WHERE m.release_id=c.release_id AND t.source_status='present' LIMIT 1) visible ON TRUE
 GROUP BY c.artist_id,c.name_id
 ), counts AS (SELECT artist_id,name_id,sum(votes) AS votes FROM (
 SELECT artist_id,name_id,votes FROM track_counts UNION ALL SELECT artist_id,name_id,votes FROM release_counts
 ) v GROUP BY artist_id,name_id),
 popular AS (SELECT DISTINCT ON (c.artist_id) c.artist_id,n.spelling FROM counts c JOIN artist_names n ON n.id=c.name_id
 ORDER BY c.artist_id,c.votes DESC,n.spelling COLLATE "C",n.id), owned AS (`+ownedNames+`),
 named AS MATERIALIZED (SELECT w.artist_id,n.id AS name_id,n.spelling FROM owned w JOIN artist_names n ON n.id=w.name_id),
 fallback AS (SELECT DISTINCT ON (artist_id) artist_id,spelling FROM named ORDER BY artist_id,spelling COLLATE "C",name_id),
 search_names AS (
 SELECT artist_id,name_id,NULL::uuid AS source_artist_id,spelling AS original FROM named
 UNION ALL SELECT redirect_artist_id,NULL,artist_id,display_name FROM artist_overrides
 WHERE redirect_artist_id=ANY($1::uuid[]) AND display_name IS NOT NULL
 ), name_lists AS (SELECT artist_id,jsonb_agg(jsonb_build_object('name_id',name_id,'source_artist_id',source_artist_id,'original',original)) AS names
 FROM search_names GROUP BY artist_id)
 SELECT a.id::text,COALESCE(pop.spelling,fallback.spelling,a.initial_name),o.display_name,COALESCE(n.names,'[]'::jsonb) FROM artists a
 LEFT JOIN artist_overrides o ON o.artist_id=a.id
 LEFT JOIN popular pop ON pop.artist_id=a.id
 LEFT JOIN fallback ON fallback.artist_id=a.id LEFT JOIN name_lists n ON n.artist_id=a.id
 WHERE a.id=ANY($1::uuid[]) ORDER BY a.id`, ids)
	if err != nil {
		return err
	}
	type profile struct {
		id, auto string
		manual   sql.NullString
		names    []byte
	}
	profiles := []profile{}
	for rows.Next() {
		var p profile
		if err = rows.Scan(&p.id, &p.auto, &p.manual, &p.names); err != nil {
			rows.Close()
			return err
		}
		profiles = append(profiles, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	type profileValue struct {
		ID        string `json:"id"`
		Auto      string `json:"auto"`
		Effective string `json:"effective"`
		Sort      string `json:"sort"`
		Initial   string `json:"initial"`
	}
	profileValues := make([]profileValue, 0, len(profiles))
	searchValues := []searchName{}
	for _, p := range profiles {
		name := p.auto
		if p.manual.Valid {
			name = p.manual.String
		}
		initial, err := InitialGroup(name)
		if err != nil {
			return err
		}
		profileValues = append(profileValues, profileValue{p.id, p.auto, name, textnorm.Text(name), initial})
		var names []searchName
		if err = json.Unmarshal(p.names, &names); err != nil {
			return err
		}
		names = append(names, searchName{Original: name})
		for _, n := range names {
			n.ArtistID = p.id
			n.Normalized = textnorm.Text(n.Original)
			n.Grams = searchindex.ShortGrams(n.Normalized)
			if loose := textnorm.Loose(n.Normalized); loose != "" && loose != n.Normalized {
				n.Loose = loose
				n.LooseGrams = searchindex.ShortGrams(loose)
			}
			searchValues = append(searchValues, n)
		}
	}
	data, err := json.Marshal(profileValues)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO artist_profiles(artist_id,auto_display_name,effective_display_name,sort_key,initial_group,version,applied_generation)
 SELECT v.id,v.auto,v.effective,v.sort,v.initial,$2,$3 FROM jsonb_to_recordset($1::jsonb)
 AS v(id uuid,auto text,effective text,sort text,initial text)
 ON CONFLICT(artist_id) DO UPDATE SET auto_display_name=EXCLUDED.auto_display_name,
 effective_display_name=EXCLUDED.effective_display_name,sort_key=EXCLUDED.sort_key,initial_group=EXCLUDED.initial_group,version=EXCLUDED.version,
 applied_generation=EXCLUDED.applied_generation`, string(data), Version, gen); err != nil {
		return err
	}
	return refreshSearchNames(ctx, tx, ids, gen, searchValues)
}

type searchName struct {
	ArtistID       string   `json:"artist_id"`
	NameID         *string  `json:"name_id"`
	SourceArtistID *string  `json:"source_artist_id"`
	Original       string   `json:"original"`
	Normalized     string   `json:"normalized"`
	Grams          []string `json:"grams"`
	Loose          string   `json:"loose"`
	LooseGrams     []string `json:"loose_grams"`
}

// 名字和主名共用同一份归属读取，避免对热门身份的全部署名再次展开。
func refreshSearchNames(ctx context.Context, tx *sql.Tx, ids []string, gen int64, values []searchName) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM artist_search_names WHERE artist_id=ANY($1::uuid[])`, ids); err != nil {
		return err
	}
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO artist_search_names(artist_id,name_id,source_artist_id,original_value,normalized_value,normalization_version,projection_generation,short_grams,loose_value,loose_grams)
 SELECT v.artist_id,v.name_id,v.source_artist_id,v.original,v.normalized,$2,$3,v.grams,NULLIF(v.loose,''),v.loose_grams FROM jsonb_to_recordset($1::jsonb)
 AS v(artist_id uuid,name_id uuid,source_artist_id uuid,original text,normalized text,grams text[],loose text,loose_grams text[]) WHERE v.normalized<>''`, string(data), textnorm.Version, gen)
	return err
}

// RefreshProfiles 仅刷新显式身份（含无当前署名的保留身份）；空批无操作，调用者仍须持CATP。
func RefreshProfiles(ctx context.Context, tx *sql.Tx, ids []string) error {
	ids = uniqueIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	if err := textnorm.LoadEmbedded(); err != nil {
		return err
	}
	gen, err := generation(ctx, tx)
	if err != nil {
		return err
	}
	return refreshProfiles(ctx, tx, ids, gen)
}
