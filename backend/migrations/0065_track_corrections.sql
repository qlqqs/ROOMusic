ALTER TABLE tracks ADD COLUMN edit_revision BIGINT NOT NULL DEFAULT 1
    CONSTRAINT tracks_edit_revision_ck CHECK (edit_revision > 0);

CREATE TABLE track_corrections (
    track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    field TEXT NOT NULL,
    mode TEXT NOT NULL,
    text_value TEXT NOT NULL,
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    PRIMARY KEY (track_id, field),
    CONSTRAINT track_corrections_field_ck CHECK (field IN ('title','artist')),
    CONSTRAINT track_corrections_mode_ck CHECK (mode = 'set'),
    CONSTRAINT track_corrections_value_ck CHECK (btrim(text_value) <> '' AND octet_length(text_value) <= 4096)
);

CREATE TABLE track_auto_values (
    track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    field TEXT NOT NULL,
    mode TEXT NOT NULL,
    text_value TEXT NOT NULL,
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    PRIMARY KEY (track_id, field),
    CONSTRAINT track_auto_values_field_ck CHECK (field IN ('title','artist')),
    CONSTRAINT track_auto_values_mode_ck CHECK (mode = 'set'),
    CONSTRAINT track_auto_values_value_ck CHECK (btrim(text_value) <> '' AND octet_length(text_value) <= 4096)
);

CREATE VIEW track_effective AS
SELECT t.id, t.medium_id, t.position,
       COALESCE(ct.text_value, atitle.text_value, t.title) AS title,
       COALESCE(ca.text_value, aa.text_value, t.artist) AS artist,
       t.disc_number, t.source_root_id, t.relative_path, t.source_status, t.observed_at,
       t.source_kind, t.source_identity, t.duration_seconds, t.codec, t.sample_rate, t.channels, t.bitrate,
       t.cue_sheet_path, t.cue_parent_relative_path, t.cue_referenced_file, t.cue_index_frames,
       t.cue_end_frames, t.cue_isrc, t.bit_depth, t.content_fingerprint, t.source_size, t.source_modified_at,
       t.lyrics, t.lyrics_source, t.work_title, t.work_catalog, t.work_index,
       t.movement_number, t.movement_title, t.work_source, t.edit_revision
FROM tracks t
LEFT JOIN track_corrections ct ON ct.track_id=t.id AND ct.field='title'
LEFT JOIN track_corrections ca ON ca.track_id=t.id AND ca.field='artist'
LEFT JOIN track_auto_values atitle ON atitle.track_id=t.id AND atitle.field='title'
LEFT JOIN track_auto_values aa ON aa.track_id=t.id AND aa.field='artist';

ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK (operation_type IN
    ('release_correction','release_auto_correction','track_correction','track_auto_correction','release_external_artwork','track_external_lyrics','external_link',
     'release_group_change','release_group_default','release_issue_review','release_claim','revert',
     'artist_merge','artist_split','artist_rename','artist_candidate_ignore'));

ALTER TABLE curation_model_exceptions DROP CONSTRAINT curation_model_exceptions_tool_check;
ALTER TABLE curation_model_exceptions ADD CONSTRAINT curation_model_exceptions_tool_check
    CHECK (tool IN ('resolve_release_match','correct_release','correct_track'));
