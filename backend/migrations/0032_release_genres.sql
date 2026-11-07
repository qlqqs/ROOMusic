-- 专辑流派改成有序列表；保留原列位置，回退时取第一项。
DROP VIEW release_effective;

ALTER TABLE releases RENAME COLUMN genre TO genres;
ALTER TABLE releases ALTER COLUMN genres TYPE TEXT[]
    USING CASE WHEN NULLIF(BTRIM(genres), '') IS NULL THEN '{}'::text[] ELSE ARRAY[BTRIM(genres)] END;
ALTER TABLE releases ALTER COLUMN genres SET DEFAULT '{}', ALTER COLUMN genres SET NOT NULL;

-- 用旧决定里已排序的候选补全流派，不做大小写、全半角合并；下次重扫按新规则整理。
UPDATE releases r SET genres = ARRAY(
    SELECT e.value FROM jsonb_array_elements_text(d.candidates) WITH ORDINALITY AS e(value, n) ORDER BY e.n)
FROM release_field_decisions d
WHERE d.release_id = r.id AND d.field_key = 'genre' AND d.reason = 'inconsistent_candidates'
  AND jsonb_typeof(d.candidates) = 'array' AND d.candidates ->> 0 = r.genres[1];

-- 曲目流派不同不再是问题。
UPDATE release_field_decisions
SET confidence = 'high', action = 'auto_apply', reason = NULL, rule_id = 'genre_union_v1'
WHERE field_key = 'genre' AND action = 'uncertain_apply';

ALTER TABLE release_corrections ADD COLUMN list_value TEXT[];
ALTER TABLE release_corrections DROP CONSTRAINT release_corrections_value_ck;
UPDATE release_corrections SET list_value = ARRAY[text_value], text_value = NULL WHERE field = 'genre' AND mode = 'set';
ALTER TABLE release_corrections ADD CONSTRAINT release_corrections_value_ck CHECK (
    (mode = 'null' AND text_value IS NULL AND year_value IS NULL AND list_value IS NULL)
    OR (mode = 'set' AND field = 'year' AND text_value IS NULL AND list_value IS NULL AND year_value BETWEEN 1 AND 9999)
    OR (mode = 'set' AND field = 'genre' AND text_value IS NULL AND year_value IS NULL
        AND list_value IS NOT NULL AND cardinality(list_value) BETWEEN 1 AND 20 AND array_position(list_value, NULL) IS NULL)
    OR (mode = 'set' AND field NOT IN ('year', 'genre') AND year_value IS NULL AND list_value IS NULL
        AND text_value IS NOT NULL AND BTRIM(text_value) <> '' AND OCTET_LENGTH(text_value) <= 4096)
);

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
