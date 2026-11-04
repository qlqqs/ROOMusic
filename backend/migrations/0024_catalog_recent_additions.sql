-- 按组读取可见发行的最早加入时间；可见性仍由 present 曲目决定。
CREATE INDEX releases_group_created_id_index ON releases(group_id, created_at, id);
