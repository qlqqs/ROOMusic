-- 按本批身份/名字顺序计票，并逐个不同名字枚举作用域别名，避免展开热门身份后重复排序。
-- 覆盖轨目与发行 subject；不改变事实、路由、主名规则或完成代次。
CREATE INDEX artist_effective_backfill_idx
 ON artist_effective_credits(artist_id,name_id,track_id,release_id);
