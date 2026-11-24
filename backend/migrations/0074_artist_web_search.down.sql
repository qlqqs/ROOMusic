DELETE FROM curation_source_settings WHERE source='web_search';
DELETE FROM curation_pending_items WHERE target_type='artist';
DELETE FROM artist_external_artworks WHERE source='web_search';
DELETE FROM artist_external_bios WHERE source='web_search';

SET CONSTRAINTS ALL IMMEDIATE;

ALTER TABLE curation_model_exceptions DROP CONSTRAINT curation_model_exceptions_tool_check,
    DROP CONSTRAINT curation_model_exceptions_status_check,
    DROP CONSTRAINT curation_model_exceptions_decider_ck,
    DROP CONSTRAINT curation_model_exceptions_decided_ck,
    ADD CONSTRAINT curation_model_exceptions_tool_check CHECK (tool IN ('resolve_release_match','correct_release','correct_track','merge_releases')),
    ADD CONSTRAINT curation_model_exceptions_status_check CHECK (status IN ('open','accepting','accepted','declined')),
    ADD CONSTRAINT curation_model_exceptions_check CHECK ((status = 'open') = (decided_by IS NULL)),
    ADD CONSTRAINT curation_model_exceptions_check1 CHECK ((status IN ('accepted','declined')) = (decided_at IS NOT NULL));

DROP INDEX curation_pending_items_artist_open_idx;
ALTER TABLE curation_pending_items DROP CONSTRAINT curation_pending_items_target_ck,
    DROP COLUMN artist_id,
    ALTER COLUMN target_id SET NOT NULL,
    ADD CONSTRAINT curation_pending_items_target_type_check CHECK (target_type='release'),
    ADD CONSTRAINT curation_pending_items_kind_check CHECK (kind IN ('release_match','recording_match','release_merge'));

ALTER TABLE artist_external_artworks DROP COLUMN citations, DROP CONSTRAINT artist_external_artworks_source_check,
    ADD CONSTRAINT artist_external_artworks_source_check CHECK (source IN ('fanarttv','theaudiodb','commons') OR source ~ '^plugin:[a-z0-9-]{3,40}$');
ALTER TABLE artist_external_bios DROP COLUMN citations, DROP CONSTRAINT artist_external_bios_source_check,
    ADD CONSTRAINT artist_external_bios_source_check CHECK (source IN ('wikipedia','theaudiodb') OR source ~ '^plugin:[a-z0-9-]{3,40}$');
