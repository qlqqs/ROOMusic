-- 回退：扫描流派与流派修正均取第一项，结构恢复到 0031；不改写历史操作记录。
DROP VIEW release_effective;

ALTER TABLE release_corrections DROP CONSTRAINT release_corrections_value_ck;
UPDATE release_corrections SET text_value = list_value[1] WHERE field = 'genre' AND mode = 'set';
ALTER TABLE release_corrections DROP COLUMN list_value;
ALTER TABLE release_corrections ADD CONSTRAINT release_corrections_value_ck CHECK (
        (mode = 'null' AND text_value IS NULL AND year_value IS NULL)
        OR (mode = 'set' AND field = 'year' AND text_value IS NULL AND year_value BETWEEN 1 AND 9999)
        OR (mode = 'set' AND field <> 'year' AND year_value IS NULL AND text_value IS NOT NULL
            AND BTRIM(text_value) <> '' AND OCTET_LENGTH(text_value) <= 4096)
);

ALTER TABLE releases ALTER COLUMN genres DROP NOT NULL, ALTER COLUMN genres DROP DEFAULT;
ALTER TABLE releases ALTER COLUMN genres TYPE TEXT USING genres[1];
ALTER TABLE releases RENAME COLUMN genres TO genre;

CREATE VIEW release_effective AS
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
    CASE WHEN c_genre.release_id IS NULL THEN r.genre ELSE c_genre.text_value END AS genre,
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
