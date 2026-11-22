CREATE OR REPLACE VIEW release_effective AS
SELECT
    r.id,
    r.group_id,
    COALESCE(c_title.text_value, r.title) AS title,
    COALESCE(c_album_artist.text_value, r.artist) AS artist,
    r.created_at,
    r.source_root_id,
    r.relative_directory,
    r.candidate_anchor,
    r.candidate_kind,
    COALESCE(c_album_artist.text_value, r.album_artist) AS album_artist,
    CASE WHEN c_year.release_id IS NULL THEN r.year ELSE c_year.year_value END AS year,
    r.source_type,
    r.media_type,
    CASE WHEN c_genre.release_id IS NULL THEN r.genres
         WHEN c_genre.mode = 'null' THEN '{}'::text[]
         ELSE c_genre.list_value END AS genres,
    CASE WHEN c_catalog_number.release_id IS NULL THEN r.catalog_number ELSE c_catalog_number.text_value END AS catalog_number,
    CASE WHEN c_edition.release_id IS NULL THEN r.edition ELSE c_edition.text_value END AS edition,
    CASE WHEN c_label.release_id IS NULL THEN r.label ELSE c_label.text_value END AS label,
    CASE WHEN c_barcode.release_id IS NULL THEN r.barcode ELSE c_barcode.text_value END AS barcode,
    r.edit_revision
FROM releases r
LEFT JOIN release_corrections c_title ON c_title.release_id = r.id AND c_title.field = 'title'
LEFT JOIN release_corrections c_album_artist ON c_album_artist.release_id = r.id AND c_album_artist.field = 'album_artist'
LEFT JOIN release_corrections c_year ON c_year.release_id = r.id AND c_year.field = 'year'
LEFT JOIN release_corrections c_genre ON c_genre.release_id = r.id AND c_genre.field = 'genre'
LEFT JOIN release_corrections c_catalog_number ON c_catalog_number.release_id = r.id AND c_catalog_number.field = 'catalog_number'
LEFT JOIN release_corrections c_edition ON c_edition.release_id = r.id AND c_edition.field = 'edition'
LEFT JOIN release_corrections c_label ON c_label.release_id = r.id AND c_label.field = 'label'
LEFT JOIN release_corrections c_barcode ON c_barcode.release_id = r.id AND c_barcode.field = 'barcode';

-- 回退会丢弃自动值、相关操作及其撤销记录。
DROP TABLE release_auto_values, curation_suppressions, curation_settings;
DELETE FROM operations WHERE reverts_operation_id IN (SELECT id FROM operations WHERE operation_type='release_auto_correction');
DELETE FROM operations WHERE operation_type='release_auto_correction';
DELETE FROM artist_credits WHERE layer='auto';
UPDATE artist_names SET origin='scan' WHERE origin='auto';

-- 先完成删除操作触发的延迟外键检查，再恢复旧约束。
SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE artist_credits DROP CONSTRAINT artist_credits_layer_check;
ALTER TABLE artist_credits ADD CONSTRAINT artist_credits_layer_check CHECK (layer IN ('scan', 'correction'));
ALTER TABLE artist_names DROP CONSTRAINT artist_names_origin_check;
ALTER TABLE artist_names ADD CONSTRAINT artist_names_origin_check CHECK (origin IN ('scan', 'correction', 'seed'));

ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK(operation_type IN
 ('release_correction','release_group_change','release_group_default','release_issue_review','release_claim','revert',
 'artist_merge','artist_split','artist_rename','artist_candidate_ignore'));

DROP INDEX operations_provider_idx, operations_batch_idx;
ALTER TABLE operations DROP COLUMN batch_id;
DROP TABLE curation_batches;
ALTER TABLE releases DROP COLUMN auto_revision;
