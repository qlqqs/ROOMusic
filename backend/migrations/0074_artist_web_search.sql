ALTER TABLE artist_external_artworks DROP CONSTRAINT artist_external_artworks_source_check,
    ADD CONSTRAINT artist_external_artworks_source_check CHECK (source IN ('fanarttv','theaudiodb','commons','web_search') OR source ~ '^plugin:[a-z0-9-]{3,40}$'),
    ADD COLUMN citations jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(citations)='array');
ALTER TABLE artist_external_bios DROP CONSTRAINT artist_external_bios_source_check,
    ADD CONSTRAINT artist_external_bios_source_check CHECK (source IN ('wikipedia','theaudiodb','web_search') OR source ~ '^plugin:[a-z0-9-]{3,40}$'),
    ADD COLUMN citations jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(citations)='array');

ALTER TABLE curation_pending_items DROP CONSTRAINT curation_pending_items_target_type_check,
    DROP CONSTRAINT curation_pending_items_kind_check,
    ALTER COLUMN target_id DROP NOT NULL,
    ADD COLUMN artist_id uuid REFERENCES artists(id) ON DELETE CASCADE,
    ADD CONSTRAINT curation_pending_items_target_ck CHECK (
        (target_type='release' AND target_id IS NOT NULL AND artist_id IS NULL AND kind IN ('release_match','recording_match','release_merge')) OR
        (target_type='artist' AND artist_id IS NOT NULL AND target_id IS NULL AND kind='artist_identity'));
CREATE UNIQUE INDEX curation_pending_items_artist_open_idx ON curation_pending_items(artist_id,kind)
    WHERE status <> 'resolved' AND target_type='artist';

ALTER TABLE curation_model_exceptions DROP CONSTRAINT curation_model_exceptions_tool_check,
    DROP CONSTRAINT curation_model_exceptions_status_check,
    DROP CONSTRAINT curation_model_exceptions_check,
    DROP CONSTRAINT curation_model_exceptions_check1,
    ADD CONSTRAINT curation_model_exceptions_tool_check CHECK (tool IN ('resolve_release_match','correct_release','correct_track','merge_releases','resolve_artist_identity')),
    ADD CONSTRAINT curation_model_exceptions_status_check CHECK (status IN ('open','accepting','accepted','declined','superseded')),
    ADD CONSTRAINT curation_model_exceptions_decider_ck CHECK (status='superseded' OR ((status='open')=(decided_by IS NULL))),
    ADD CONSTRAINT curation_model_exceptions_decided_ck CHECK ((status IN ('accepted','declined','superseded'))=(decided_at IS NOT NULL));
