WITH external_operations AS (
    SELECT id FROM operations WHERE operation_type IN ('release_external_artwork','track_external_lyrics')
)
UPDATE steward_plan_items SET operation_id = NULL
WHERE operation_id IN (SELECT id FROM external_operations
    UNION ALL SELECT id FROM operations WHERE reverts_operation_id IN (SELECT id FROM external_operations));

DELETE FROM curation_suppressions WHERE subject IN ('artwork','artwork_source','lyrics','lyrics_source');
DROP TABLE release_external_artworks, track_external_lyrics;

DELETE FROM operations WHERE reverts_operation_id IN (
    SELECT id FROM operations WHERE operation_type IN ('release_external_artwork','track_external_lyrics')
);
DELETE FROM operations WHERE operation_type IN ('release_external_artwork','track_external_lyrics');

SET CONSTRAINTS ALL IMMEDIATE;
ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK(operation_type IN
 ('release_correction','release_auto_correction','release_group_change','release_group_default','release_issue_review','release_claim','revert',
 'artist_merge','artist_split','artist_rename','artist_candidate_ignore'));
