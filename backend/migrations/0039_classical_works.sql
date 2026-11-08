-- 作品与乐章属于扫描事实，已有曲目保持空值，重扫时整体替换。
ALTER TABLE tracks
    ADD COLUMN work_title TEXT CHECK (work_title IS NULL OR (btrim(work_title) <> '' AND octet_length(work_title) <= 4096)),
    ADD COLUMN work_catalog TEXT CHECK (work_catalog IS NULL OR (btrim(work_catalog) <> '' AND octet_length(work_catalog) <= 256)),
    ADD COLUMN work_index INTEGER CHECK (work_index IS NULL OR work_index > 0),
    ADD COLUMN movement_number INTEGER CHECK (movement_number IS NULL OR movement_number > 0),
    ADD COLUMN movement_title TEXT CHECK (movement_title IS NULL OR (btrim(movement_title) <> '' AND octet_length(movement_title) <= 4096)),
    ADD COLUMN work_source TEXT CHECK (work_source IS NULL OR work_source IN ('tag','title')),
    ADD CONSTRAINT tracks_work_ck CHECK (
        (work_title IS NULL) = (work_source IS NULL)
        AND (work_title IS NOT NULL OR (work_catalog IS NULL AND work_index IS NULL AND movement_number IS NULL AND movement_title IS NULL)));

ALTER TABLE catalog_search_values DROP CONSTRAINT catalog_search_values_field_ck;
ALTER TABLE catalog_search_values ADD CONSTRAINT catalog_search_values_field_ck CHECK (
    (release_id IS NOT NULL AND field IN ('release_title','release_artist','album_artist','label','catalog_number')) OR
    (track_id IS NOT NULL AND field IN ('track_title','track_artist','credit','work_title','work_catalog')));
