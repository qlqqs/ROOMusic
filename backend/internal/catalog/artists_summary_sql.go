package catalog

// 摘要计数只需要跨角色去重的 present 曲目，不展开“角色 × 每首曲目”的参与明细。
// 先去重来源，再按实际曲目取并集；曲目只属于一个发行，UNION 后 count(*) 即轨数。
// 角色最多九项，分别用存在性判断保留全角色参与，不从计数集合反推或丢弃角色。
// artistParticipationCTE 仍供含 missing 的详情和参与明细使用，不改变那些读取的合同。
const artistSummarySQL = `WITH summary_release_subjects AS MATERIALIZED (
 SELECT DISTINCT artist_id,release_id FROM artist_effective_credits
 WHERE artist_id=ANY($1::uuid[]) AND release_id IS NOT NULL
), summary_track_subjects AS MATERIALIZED (
 SELECT DISTINCT artist_id,track_id FROM artist_effective_credits
 WHERE artist_id=ANY($1::uuid[]) AND track_id IS NOT NULL
), summary_tracks AS MATERIALIZED (
 SELECT e.artist_id,t.id AS track_id,m.release_id FROM summary_track_subjects e
 JOIN tracks t ON t.id=e.track_id JOIN media m ON m.id=t.medium_id WHERE t.source_status='present'
 UNION
 SELECT e.artist_id,t.id,m.release_id FROM summary_release_subjects e
 JOIN media m ON m.release_id=e.release_id JOIN tracks t ON t.medium_id=m.id WHERE t.source_status='present'
), counts AS (
 SELECT s.artist_id,count(DISTINCT r.group_id) AS albums,count(*) AS tracks
 FROM summary_tracks s JOIN releases r ON r.id=s.release_id GROUP BY s.artist_id
) SELECT a.id::text,p.effective_display_name,p.initial_group,COALESCE(c.albums,0),COALESCE(c.tracks,0),
 to_json(ARRAY(SELECT role FROM (VALUES ('release_artist'),('album_artist'),('track_artist'),('lyricist'),('composer'),('arranger'),('conductor'),('performer'),('producer')) roles(role)
 WHERE EXISTS (SELECT 1 FROM artist_effective_credits e WHERE e.artist_id=a.id AND e.role=roles.role AND (
 EXISTS (SELECT 1 FROM tracks t WHERE t.id=e.track_id AND t.source_status='present') OR
 EXISTS (SELECT 1 FROM media m JOIN tracks t ON t.medium_id=m.id WHERE m.release_id=e.release_id AND t.source_status='present')))
 ORDER BY ` + artistRoleOrder + `))
 FROM artists a JOIN artist_profiles p ON p.artist_id=a.id LEFT JOIN counts c ON c.artist_id=a.id
 WHERE a.id=ANY($1::uuid[])`
