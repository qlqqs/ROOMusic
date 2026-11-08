-- 先清掉旧版本不认识的搜索值，再恢复字段约束。
DELETE FROM catalog_search_values WHERE field IN ('work_title','work_catalog');
ALTER TABLE catalog_search_values DROP CONSTRAINT catalog_search_values_field_ck;
ALTER TABLE catalog_search_values ADD CONSTRAINT catalog_search_values_field_ck CHECK (
    (release_id IS NOT NULL AND field IN ('release_title','release_artist','album_artist','label','catalog_number')) OR
    (track_id IS NOT NULL AND field IN ('track_title','track_artist','credit')));

ALTER TABLE tracks
    DROP CONSTRAINT tracks_work_ck,
    DROP COLUMN work_title,
    DROP COLUMN work_catalog,
    DROP COLUMN work_index,
    DROP COLUMN movement_number,
    DROP COLUMN movement_title,
    DROP COLUMN work_source;
