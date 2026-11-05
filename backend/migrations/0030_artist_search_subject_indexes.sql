-- 热门姓名的命中说明必须同时按身份与来源定位，不能先展开整位艺人的署名再过滤。
-- 仅新增读取索引，不改事实、派生代次或发布状态；沿用统一事务迁移。
CREATE INDEX artist_effective_search_track_idx
 ON artist_effective_credits(artist_id,track_id) INCLUDE(release_id,role)
 WHERE track_id IS NOT NULL;
CREATE INDEX artist_effective_search_release_idx
 ON artist_effective_credits(artist_id,release_id) INCLUDE(track_id,role)
 WHERE release_id IS NOT NULL;
