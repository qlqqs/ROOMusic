package releasesql

// ArtistRefsReady 与艺人协议 v1 一致；状态检查和 refs 子查询始终处于同一条 SQL 的快照。
// 缺行、未封存、代次或规则版本不匹配都只展示原文，不读取正在重建的图。
const ArtistRefsReady = `COALESCE((SELECT status='ready' AND manifest_sealed
 AND target_generation=published_generation AND textnorm_version=1 AND published_textnorm_version=1
 AND search_projection_version=1 AND published_search_projection_version=1
 FROM catalog_projection_state WHERE capability='artists'),FALSE)`

// 角色仅用于查询选择，不进入 ArtistRef DTO：release_artist、album_artist、track_artist、composer、conductor、performer、producer。
// 每个数组最多传出 101 项供 Go 检测超限，绝不静默截成 100 项。
// 身份只来自 effective_credits；原文来自对应 name，不能按名字重新解析全局绑定而丢失作用域拆分。
const artistRefsObject = `jsonb_build_object('artist_id',refs.artist_id::text,'name',refs.spelling,
 'display_name',refs.effective_display_name)`

const releaseArtistRefsProjection = `CASE WHEN ` + ArtistRefsReady + ` THEN COALESCE((
 SELECT jsonb_agg(` + artistRefsObject + ` ORDER BY refs.position,refs.spelling COLLATE "C",refs.artist_id)
 FROM (SELECT e.artist_id,n.spelling,p.effective_display_name,e.role,e.position
 FROM artist_effective_credits e JOIN artist_names n ON n.id=e.name_id
 LEFT JOIN artist_profiles p ON p.artist_id=e.artist_id
 WHERE e.release_id=releases.id AND e.role='release_artist'
 ORDER BY e.position,n.spelling COLLATE "C",e.artist_id LIMIT 101) refs),'[]'::jsonb) ELSE '[]'::jsonb END`

const releaseAlbumArtistRefsProjection = `CASE WHEN ` + ArtistRefsReady + ` THEN COALESCE((
 SELECT jsonb_agg(` + artistRefsObject + ` ORDER BY refs.position,refs.spelling COLLATE "C",refs.artist_id)
 FROM (SELECT e.artist_id,n.spelling,p.effective_display_name,e.role,e.position
 FROM artist_effective_credits e JOIN artist_names n ON n.id=e.name_id
 LEFT JOIN artist_profiles p ON p.artist_id=e.artist_id
 WHERE e.release_id=releases.id AND e.role='album_artist'
 ORDER BY e.position,n.spelling COLLATE "C",e.artist_id LIMIT 101) refs),'[]'::jsonb) ELSE '[]'::jsonb END`

const releaseSummaryArtistRefsProjection = releaseArtistRefsProjection + `,` + releaseAlbumArtistRefsProjection

// TrackArtistRefsProjection 要求外层曲目别名为 t；含 missing，不依赖文件存在性。
// 曲目艺人展示串由写者按原文排序，refs 同样使用 C 排序，不能套主名排序或原始标签次序。
const TrackArtistRefsProjection = `CASE WHEN ` + ArtistRefsReady + ` THEN COALESCE((
 SELECT jsonb_agg(` + artistRefsObject + ` ORDER BY refs.spelling COLLATE "C",refs.artist_id)
 FROM (SELECT e.artist_id,n.spelling,p.effective_display_name,e.role,e.position
 FROM artist_effective_credits e JOIN artist_names n ON n.id=e.name_id
 LEFT JOIN artist_profiles p ON p.artist_id=e.artist_id
 WHERE e.track_id=t.id AND e.role='track_artist'
 ORDER BY n.spelling COLLATE "C",e.artist_id LIMIT 101) refs),'[]'::jsonb) ELSE '[]'::jsonb END`

// TrackCreditArtistRefsProjection 要求外层为 t、c；角色和原写法必须同时匹配，禁止把同角色的其他人串入。
const TrackCreditArtistRefsProjection = `CASE WHEN ` + ArtistRefsReady + ` THEN COALESCE((
 SELECT jsonb_agg(` + artistRefsObject + ` ORDER BY refs.position,refs.artist_id)
 FROM (SELECT e.artist_id,n.spelling,p.effective_display_name,e.role,e.position
 FROM artist_effective_credits e JOIN artist_names n ON n.id=e.name_id
 LEFT JOIN artist_profiles p ON p.artist_id=e.artist_id
 WHERE e.track_id=t.id AND e.role=c.role AND n.spelling=c.name
 ORDER BY e.position,e.artist_id LIMIT 101) refs),'[]'::jsonb) ELSE '[]'::jsonb END`

// ReleaseCreditArtistRefsProjection 要求外层为 credits，$1 是发行 ID。
// album_artist 的展示串可能聚合多名；其他署名按角色及原文汇总本发行内曲目的有效身份，
// 同名但已按作用域拆开的两位艺人都保留，不用全局名字键覆盖身份。
const ReleaseCreditArtistRefsProjection = `CASE WHEN ` + ArtistRefsReady + ` THEN COALESCE((
 SELECT jsonb_agg(` + artistRefsObject + ` ORDER BY refs.spelling COLLATE "C",refs.artist_id)
 FROM (SELECT DISTINCT e.artist_id,n.spelling COLLATE "C" AS spelling,p.effective_display_name,e.role
 FROM (
 SELECT artist_id,name_id,role FROM artist_effective_credits
 WHERE release_id=$1::uuid AND role=credits.role AND credits.role IN ('release_artist','album_artist')
 UNION ALL
 SELECT e.artist_id,e.name_id,e.role FROM media m JOIN tracks t ON t.medium_id=m.id
 JOIN artist_effective_credits e ON e.track_id=t.id
 WHERE m.release_id=$1::uuid AND t.source_status='present' AND e.role=credits.role
 AND credits.role IN ('composer','conductor','performer','producer')
 ) e JOIN artist_names n ON n.id=e.name_id LEFT JOIN artist_profiles p ON p.artist_id=e.artist_id
 WHERE n.spelling=credits.name OR (credits.role='album_artist' AND credits.name=(
 SELECT album_artist FROM release_effective WHERE id=$1::uuid))
 ORDER BY n.spelling COLLATE "C",e.artist_id LIMIT 101) refs),'[]'::jsonb) ELSE '[]'::jsonb END`

// BatchSummaryArtistRefsCTE 仅聚合 summary_release_ids 指定的发行，两个角色一次读取。
// 窗口每角色最多保留 101 项供边界校验；避免两个相关子查询被估算为每发行重扫而触发 JIT。
const BatchSummaryArtistRefsCTE = `summary_artist_refs AS MATERIALIZED (
 SELECT refs.release_id,
 COALESCE(jsonb_agg(` + artistRefsObject + ` ORDER BY refs.position,refs.spelling COLLATE "C",refs.artist_id)
 FILTER (WHERE refs.role='release_artist'),'[]'::jsonb) AS artist_refs,
 COALESCE(jsonb_agg(` + artistRefsObject + ` ORDER BY refs.position,refs.spelling COLLATE "C",refs.artist_id)
 FILTER (WHERE refs.role='album_artist'),'[]'::jsonb) AS album_artist_refs
 FROM (SELECT e.release_id,e.artist_id,n.spelling,p.effective_display_name,e.role,e.position,
 row_number() OVER (PARTITION BY e.release_id,e.role ORDER BY e.position,n.spelling COLLATE "C",e.artist_id) AS ordinal
 FROM summary_release_ids ids JOIN artist_effective_credits e ON e.release_id=ids.id
 JOIN artist_names n ON n.id=e.name_id LEFT JOIN artist_profiles p ON p.artist_id=e.artist_id
 WHERE ` + ArtistRefsReady + ` AND e.role IN ('release_artist','album_artist')) refs
 WHERE refs.ordinal<=101 GROUP BY refs.release_id
)`

// SelectedTrackArtistRefsCTE 只读取本批 selected_tracks，不按曲目逐项执行相关图查询。
const SelectedTrackArtistRefsCTE = `track_artist_refs AS MATERIALIZED (
 SELECT refs.track_id,jsonb_agg(` + artistRefsObject + ` ORDER BY refs.spelling COLLATE "C",refs.artist_id) AS artist_refs
 FROM (SELECT e.track_id,e.artist_id,n.spelling,p.effective_display_name,e.position,
 row_number() OVER (PARTITION BY e.track_id ORDER BY n.spelling COLLATE "C",e.artist_id) AS ordinal
 FROM selected_tracks t JOIN artist_effective_credits e ON e.track_id=t.id
 JOIN artist_names n ON n.id=e.name_id LEFT JOIN artist_profiles p ON p.artist_id=e.artist_id
 WHERE ` + ArtistRefsReady + ` AND e.role='track_artist') refs
 WHERE refs.ordinal<=101 GROUP BY refs.track_id
)`
