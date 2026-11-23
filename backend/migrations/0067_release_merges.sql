CREATE TABLE release_merges (
    source_release_id UUID PRIMARY KEY REFERENCES releases(id) ON DELETE RESTRICT,
    target_release_id UUID NOT NULL REFERENCES releases(id) ON DELETE RESTRICT,
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    merged_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK (source_release_id <> target_release_id),
    UNIQUE (source_release_id, target_release_id)
);
CREATE INDEX release_merges_target_idx ON release_merges(target_release_id);

ALTER TABLE media
    ADD COLUMN origin_release_id UUID,
    ADD COLUMN origin_position INTEGER,
    ADD CONSTRAINT media_origin_ck CHECK ((origin_release_id IS NULL) = (origin_position IS NULL)),
    ADD CONSTRAINT media_origin_fk FOREIGN KEY (origin_release_id, release_id)
        REFERENCES release_merges(source_release_id, target_release_id) ON DELETE RESTRICT;
CREATE UNIQUE INDEX media_origin_position_uq ON media(origin_release_id, origin_position)
    WHERE origin_release_id IS NOT NULL;

CREATE TABLE operations_release_targets (
    operation_id UUID REFERENCES operations(id) ON DELETE CASCADE DEFERRABLE INITIALLY DEFERRED,
    release_id UUID NOT NULL,
    PRIMARY KEY (operation_id, release_id)
);
CREATE INDEX operations_release_targets_release_idx ON operations_release_targets(release_id, operation_id);

ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK (operation_type IN
    ('release_correction','release_auto_correction','track_correction','track_auto_correction','release_external_artwork','track_external_lyrics','external_link',
     'release_group_change','release_group_default','release_issue_review','release_claim','revert',
     'artist_merge','artist_split','artist_rename','artist_candidate_ignore','release_merge','release_auto_merge'));

ALTER TABLE curation_pending_items DROP CONSTRAINT curation_pending_items_kind_check;
ALTER TABLE curation_pending_items ADD CONSTRAINT curation_pending_items_kind_check
    CHECK (kind IN ('release_match','recording_match','release_merge'));

ALTER TABLE curation_model_exceptions DROP CONSTRAINT curation_model_exceptions_tool_check;
ALTER TABLE curation_model_exceptions ADD CONSTRAINT curation_model_exceptions_tool_check
    CHECK (tool IN ('resolve_release_match','correct_release','correct_track','merge_releases'));
