ALTER TABLE releases ADD COLUMN scan_version_terms text[] NOT NULL DEFAULT '{}';
ALTER TABLE tracks ADD COLUMN sort_position integer CHECK (sort_position > 0);
ALTER TABLE artist_profiles ADD COLUMN list_sort_key text NOT NULL DEFAULT '';

CREATE TABLE release_attachments (
    release_id uuid NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    relative_path text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('image','document')),
    image_format text, width integer, height integer,
    PRIMARY KEY (release_id, relative_path),
    CHECK ((kind='document' AND image_format IS NULL AND width IS NULL AND height IS NULL)
        OR (kind='image' AND image_format IS NOT NULL AND width IS NOT NULL AND width>0 AND height IS NOT NULL AND height>0))
);

CREATE FUNCTION catalog_list_sort_key(raw text) RETURNS text
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
    WITH folded AS (
        SELECT normalize(casefold(raw COLLATE pg_unicode_fast), NFD) AS value
    ), cleaned AS (
        SELECT btrim(regexp_replace(regexp_replace(value,
            U&'[[:punct:]\0300-\036F\1AB0-\1AFF\1DC0-\1DFF\20D0-\20FF\FE20-\FE2F]', '', 'g'),
            '[[:space:]]+', ' ', 'g')) AS value FROM folded
    )
    SELECT normalize(CASE WHEN value LIKE 'the %' THEN substr(value,5)||' the' ELSE value END,NFC) FROM cleaned
$$;
CREATE OR REPLACE VIEW track_effective AS
SELECT t.id, t.medium_id, t.position,
       COALESCE(ct.text_value, atitle.text_value, t.title) AS title,
       COALESCE(ca.text_value, aa.text_value, t.artist) AS artist,
       t.disc_number, t.source_root_id, t.relative_path, t.source_status, t.observed_at,
       t.source_kind, t.source_identity, t.duration_seconds, t.codec, t.sample_rate, t.channels, t.bitrate,
       t.cue_sheet_path, t.cue_parent_relative_path, t.cue_referenced_file, t.cue_index_frames,
       t.cue_end_frames, t.cue_isrc, t.bit_depth, t.content_fingerprint, t.source_size, t.source_modified_at,
       t.lyrics, t.lyrics_source, t.work_title, t.work_catalog, t.work_index,
       t.movement_number, t.movement_title, t.work_source, t.edit_revision, t.scan_tags, t.sort_position
FROM tracks t
LEFT JOIN track_corrections ct ON ct.track_id=t.id AND ct.field='title'
LEFT JOIN track_corrections ca ON ca.track_id=t.id AND ca.field='artist'
LEFT JOIN track_auto_values atitle ON atitle.track_id=t.id AND atitle.field='title'
LEFT JOIN track_auto_values aa ON aa.track_id=t.id AND aa.field='artist';

UPDATE catalog_projection_state
SET target_generation=target_generation+1, build_id=gen_random_uuid(), status='preparing',
    catalog_epoch=catalog_epoch+1, manifest_sealed=false, live_updated=0, started_at=now(), updated_at=now()
WHERE capability='artists' AND EXISTS (SELECT FROM artist_profiles);
