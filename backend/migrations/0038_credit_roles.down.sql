-- 旧版本不认识新增角色；有效署名由 artist_credits 的外键级联删除。
DELETE FROM track_credits WHERE role IN ('lyricist','arranger');
DELETE FROM artist_credits WHERE role IN ('lyricist','arranger');
DELETE FROM artist_tag_evidence WHERE role IN ('lyricist','arranger');
DELETE FROM catalog_search_values WHERE role IN ('lyricist','arranger');

-- 恢复旧唯一键前，每组保留最靠前的署名；位置相同时按 ID 稳定选择。
WITH ranked AS (
    SELECT id, row_number() OVER (PARTITION BY track_id, role, name ORDER BY position, id) AS ordinal
    FROM track_credits
)
DELETE FROM track_credits USING ranked
WHERE track_credits.id = ranked.id AND ranked.ordinal > 1;
ALTER TABLE track_credits DROP CONSTRAINT track_credits_track_role_name_detail_key;
ALTER TABLE track_credits DROP COLUMN detail;
ALTER TABLE track_credits DROP COLUMN source;
ALTER TABLE track_credits ADD CONSTRAINT track_credits_track_id_role_name_key
    UNIQUE (track_id, role, name);
ALTER TABLE track_credits DROP CONSTRAINT track_credits_role_check;
ALTER TABLE track_credits ADD CONSTRAINT track_credits_role_check
    CHECK (role IN ('composer','conductor','performer','producer'));

ALTER TABLE artist_credits DROP CONSTRAINT artist_credits_check1;
ALTER TABLE artist_credits ADD CONSTRAINT artist_credits_check1 CHECK (
    (release_id IS NOT NULL AND role IN ('release_artist','album_artist')) OR
    (track_id IS NOT NULL AND role IN ('track_artist','composer','conductor','performer','producer')));
ALTER TABLE artist_tag_evidence DROP CONSTRAINT artist_tag_evidence_role_check;
ALTER TABLE artist_tag_evidence ADD CONSTRAINT artist_tag_evidence_role_check
    CHECK (role IN ('track_artist','album_artist','composer','conductor','performer','producer'));
ALTER TABLE catalog_search_values DROP CONSTRAINT catalog_search_values_role_ck;
ALTER TABLE catalog_search_values ADD CONSTRAINT catalog_search_values_role_ck CHECK (
    (field = 'credit' AND role IS NOT NULL AND role IN ('composer','conductor','performer','producer')) OR
    (field <> 'credit' AND role IS NULL));
