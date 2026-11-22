CREATE TABLE release_auto_values (
    release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    field TEXT NOT NULL,
    mode TEXT NOT NULL,
    text_value TEXT,
    year_value INTEGER,
    list_value TEXT[],
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    PRIMARY KEY (release_id, field),
    CONSTRAINT release_auto_values_field_ck CHECK (field IN ('title', 'album_artist', 'year', 'genre', 'label', 'catalog_number', 'edition', 'barcode')),
    CONSTRAINT release_auto_values_mode_ck CHECK (mode IN ('set', 'null')),
    CONSTRAINT release_auto_values_required_field_ck CHECK (NOT (field IN ('title', 'album_artist') AND mode = 'null')),
    CONSTRAINT release_auto_values_value_ck CHECK (
        (mode = 'null' AND text_value IS NULL AND year_value IS NULL AND list_value IS NULL)
        OR (mode = 'set' AND field = 'year' AND text_value IS NULL AND list_value IS NULL AND year_value BETWEEN 1 AND 9999)
        OR (mode = 'set' AND field = 'genre' AND text_value IS NULL AND year_value IS NULL
            AND list_value IS NOT NULL AND cardinality(list_value) BETWEEN 1 AND 20 AND array_position(list_value, NULL) IS NULL)
        OR (mode = 'set' AND field NOT IN ('year', 'genre') AND year_value IS NULL AND list_value IS NULL
            AND text_value IS NOT NULL AND BTRIM(text_value) <> '' AND OCTET_LENGTH(text_value) <= 4096)
    )
);

ALTER TABLE releases ADD COLUMN auto_revision BIGINT NOT NULL DEFAULT 1;

CREATE OR REPLACE VIEW release_effective AS
SELECT
    r.id,
    r.group_id,
    COALESCE(c_title.text_value, a_title.text_value, r.title) AS title,
    COALESCE(c_album_artist.text_value, a_album_artist.text_value, r.artist) AS artist,
    r.created_at,
    r.source_root_id,
    r.relative_directory,
    r.candidate_anchor,
    r.candidate_kind,
    COALESCE(c_album_artist.text_value, a_album_artist.text_value, r.album_artist) AS album_artist,
    CASE WHEN c_year.release_id IS NOT NULL THEN c_year.year_value
         WHEN a_year.release_id IS NOT NULL THEN a_year.year_value ELSE r.year END AS year,
    r.source_type,
    r.media_type,
    CASE WHEN c_genre.release_id IS NOT NULL THEN COALESCE(c_genre.list_value, '{}'::text[])
         WHEN a_genre.release_id IS NOT NULL THEN COALESCE(a_genre.list_value, '{}'::text[]) ELSE r.genres END AS genres,
    CASE WHEN c_catalog_number.release_id IS NOT NULL THEN c_catalog_number.text_value
         WHEN a_catalog_number.release_id IS NOT NULL THEN a_catalog_number.text_value ELSE r.catalog_number END AS catalog_number,
    CASE WHEN c_edition.release_id IS NOT NULL THEN c_edition.text_value
         WHEN a_edition.release_id IS NOT NULL THEN a_edition.text_value ELSE r.edition END AS edition,
    CASE WHEN c_label.release_id IS NOT NULL THEN c_label.text_value
         WHEN a_label.release_id IS NOT NULL THEN a_label.text_value ELSE r.label END AS label,
    CASE WHEN c_barcode.release_id IS NOT NULL THEN c_barcode.text_value
         WHEN a_barcode.release_id IS NOT NULL THEN a_barcode.text_value ELSE r.barcode END AS barcode,
    r.edit_revision
FROM releases r
LEFT JOIN release_corrections c_title ON c_title.release_id = r.id AND c_title.field = 'title'
LEFT JOIN release_corrections c_album_artist ON c_album_artist.release_id = r.id AND c_album_artist.field = 'album_artist'
LEFT JOIN release_corrections c_year ON c_year.release_id = r.id AND c_year.field = 'year'
LEFT JOIN release_corrections c_genre ON c_genre.release_id = r.id AND c_genre.field = 'genre'
LEFT JOIN release_corrections c_catalog_number ON c_catalog_number.release_id = r.id AND c_catalog_number.field = 'catalog_number'
LEFT JOIN release_corrections c_edition ON c_edition.release_id = r.id AND c_edition.field = 'edition'
LEFT JOIN release_corrections c_label ON c_label.release_id = r.id AND c_label.field = 'label'
LEFT JOIN release_corrections c_barcode ON c_barcode.release_id = r.id AND c_barcode.field = 'barcode'
LEFT JOIN release_auto_values a_title ON a_title.release_id = r.id AND a_title.field = 'title'
LEFT JOIN release_auto_values a_album_artist ON a_album_artist.release_id = r.id AND a_album_artist.field = 'album_artist'
LEFT JOIN release_auto_values a_year ON a_year.release_id = r.id AND a_year.field = 'year'
LEFT JOIN release_auto_values a_genre ON a_genre.release_id = r.id AND a_genre.field = 'genre'
LEFT JOIN release_auto_values a_catalog_number ON a_catalog_number.release_id = r.id AND a_catalog_number.field = 'catalog_number'
LEFT JOIN release_auto_values a_edition ON a_edition.release_id = r.id AND a_edition.field = 'edition'
LEFT JOIN release_auto_values a_label ON a_label.release_id = r.id AND a_label.field = 'label'
LEFT JOIN release_auto_values a_barcode ON a_barcode.release_id = r.id AND a_barcode.field = 'barcode';

ALTER TABLE artist_credits DROP CONSTRAINT artist_credits_layer_check;
ALTER TABLE artist_credits ADD CONSTRAINT artist_credits_layer_check CHECK (layer IN ('scan', 'correction', 'auto'));
ALTER TABLE artist_names DROP CONSTRAINT artist_names_origin_check;
ALTER TABLE artist_names ADD CONSTRAINT artist_names_origin_check CHECK (origin IN ('scan', 'correction', 'seed', 'auto'));

ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK(operation_type IN
 ('release_correction','release_auto_correction','release_group_change','release_group_default','release_issue_review','release_claim','revert',
 'artist_merge','artist_split','artist_rename','artist_candidate_ignore'));

CREATE TABLE curation_batches (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tier TEXT NOT NULL CHECK (tier IN ('program', 'model')),
    opened_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at TIMESTAMPTZ,
    actor_id UUID NOT NULL REFERENCES users(id)
);
ALTER TABLE operations ADD COLUMN batch_id UUID REFERENCES curation_batches(id);
CREATE INDEX operations_batch_idx ON operations (batch_id, created_at DESC, id DESC) WHERE batch_id IS NOT NULL;
CREATE INDEX operations_provider_idx ON operations ((origin->>'provider'), created_at DESC, id DESC) WHERE origin->>'source' = 'curation';

CREATE TABLE curation_suppressions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_type TEXT NOT NULL,
    target_id UUID NOT NULL,
    subject TEXT NOT NULL,
    value_hash TEXT NOT NULL,
    value JSONB NOT NULL,
    operation_id UUID REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    created_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (target_type, target_id, subject, value_hash)
);
CREATE TABLE curation_settings (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    enabled BOOLEAN NOT NULL DEFAULT false,
    enabled_by UUID REFERENCES users(id),
    enabled_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO curation_settings(id) VALUES (1);
