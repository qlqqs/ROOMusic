WITH link_operations AS (
    SELECT id FROM operations WHERE operation_type = 'external_link'
)
UPDATE steward_plan_items SET operation_id = NULL
WHERE operation_id IN (SELECT id FROM link_operations
    UNION ALL SELECT id FROM operations WHERE reverts_operation_id IN (SELECT id FROM link_operations));

DELETE FROM curation_suppressions WHERE subject IN ('external_link','group_join');
DELETE FROM external_links WHERE operation_id IN (SELECT id FROM operations WHERE operation_type = 'external_link');
DROP TABLE curation_pending_items, curation_work_states;

DELETE FROM operations WHERE reverts_operation_id IN (SELECT id FROM operations WHERE operation_type = 'external_link');
DELETE FROM operations WHERE operation_type = 'external_link';

SET CONSTRAINTS ALL IMMEDIATE;
ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK(operation_type IN
 ('release_correction','release_auto_correction','release_external_artwork','track_external_lyrics','release_group_change','release_group_default','release_issue_review','release_claim','revert',
 'artist_merge','artist_split','artist_rename','artist_candidate_ignore'));

ALTER TABLE curation_settings DROP COLUMN paused;
