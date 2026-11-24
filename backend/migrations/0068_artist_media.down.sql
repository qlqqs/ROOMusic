WITH media_operations AS (
    SELECT id FROM operations WHERE operation_type IN ('artist_external_artwork','artist_external_bio')
)
UPDATE steward_plan_items SET operation_id=NULL
WHERE operation_id IN (SELECT id FROM media_operations
    UNION ALL SELECT id FROM operations WHERE reverts_operation_id IN (SELECT id FROM media_operations));

DROP TABLE artist_external_artworks, artist_external_bios;
DELETE FROM curation_suppressions WHERE target_type='artist' AND subject IN ('artist_image','artist_bio');
DELETE FROM curation_work_states WHERE subject='artist_media';
DELETE FROM operations WHERE reverts_operation_id IN (
    SELECT id FROM operations WHERE operation_type IN ('artist_external_artwork','artist_external_bio')
);
DELETE FROM operations WHERE operation_type IN ('artist_external_artwork','artist_external_bio');

SET CONSTRAINTS ALL IMMEDIATE;
ALTER TABLE artists DROP COLUMN auto_revision;
ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK (operation_type IN
    ('release_correction','release_auto_correction','track_correction','track_auto_correction','release_external_artwork','track_external_lyrics','external_link',
     'release_group_change','release_group_default','release_issue_review','release_claim','revert',
     'artist_merge','artist_split','artist_rename','artist_candidate_ignore','release_merge','release_auto_merge'));

ALTER TABLE curation_work_states DROP CONSTRAINT curation_work_states_subject_check;
ALTER TABLE curation_work_states ADD CONSTRAINT curation_work_states_subject_check
    CHECK (subject IN ('release','artwork','lyrics'));
