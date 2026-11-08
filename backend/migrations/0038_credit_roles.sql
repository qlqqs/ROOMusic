-- 扩充曲目署名角色；细分只属于曲目署名，不改变艺人身份。
ALTER TABLE track_credits DROP CONSTRAINT track_credits_role_check;
ALTER TABLE track_credits ADD CONSTRAINT track_credits_role_check
    CHECK (role IN ('lyricist','composer','arranger','conductor','performer','producer'));
ALTER TABLE track_credits ADD COLUMN detail TEXT
    CHECK (detail IS NULL OR (btrim(detail) <> '' AND octet_length(detail) <= 256));
ALTER TABLE track_credits ADD COLUMN source TEXT NOT NULL DEFAULT 'tag'
    CHECK (source IN ('tag','lyrics'));
ALTER TABLE track_credits DROP CONSTRAINT track_credits_track_id_role_name_key;
ALTER TABLE track_credits ADD CONSTRAINT track_credits_track_role_name_detail_key
    UNIQUE NULLS NOT DISTINCT (track_id, role, name, detail);

-- 0026 的第二个未命名表级 CHECK 约束角色与来源层级。
ALTER TABLE artist_credits DROP CONSTRAINT artist_credits_check1;
ALTER TABLE artist_credits ADD CONSTRAINT artist_credits_check1 CHECK (
    (release_id IS NOT NULL AND role IN ('release_artist','album_artist')) OR
    (track_id IS NOT NULL AND role IN ('track_artist','lyricist','composer','arranger','conductor','performer','producer')));
ALTER TABLE artist_tag_evidence DROP CONSTRAINT artist_tag_evidence_role_check;
ALTER TABLE artist_tag_evidence ADD CONSTRAINT artist_tag_evidence_role_check
    CHECK (role IN ('track_artist','album_artist','lyricist','composer','arranger','conductor','performer','producer'));
ALTER TABLE catalog_search_values DROP CONSTRAINT catalog_search_values_role_ck;
ALTER TABLE catalog_search_values ADD CONSTRAINT catalog_search_values_role_ck CHECK (
    (field = 'credit' AND role IS NOT NULL AND role IN ('lyricist','composer','arranger','conductor','performer','producer')) OR
    (field <> 'credit' AND role IS NULL));
