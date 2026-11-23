-- 此回退丢弃曲目修正与相关记录，回退后必须完整重扫以重建投影。
DROP VIEW track_effective;

WITH track_operations AS (
    SELECT id FROM operations WHERE operation_type IN ('track_correction','track_auto_correction')
)
UPDATE steward_plan_items SET operation_id = NULL
WHERE operation_id IN (SELECT id FROM track_operations
    UNION ALL SELECT id FROM operations WHERE reverts_operation_id IN (SELECT id FROM track_operations));

DELETE FROM curation_suppressions WHERE target_type='track' AND subject IN ('title','artist');
DELETE FROM curation_model_exceptions WHERE tool='correct_track';
DROP TABLE track_corrections, track_auto_values;
DELETE FROM artist_credits WHERE track_id IS NOT NULL AND role='track_artist' AND layer IN ('correction','auto');

DELETE FROM operations WHERE reverts_operation_id IN (
    SELECT id FROM operations WHERE operation_type IN ('track_correction','track_auto_correction')
);
DELETE FROM operations WHERE operation_type IN ('track_correction','track_auto_correction');

SET CONSTRAINTS ALL IMMEDIATE;
ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK (operation_type IN
    ('release_correction','release_auto_correction','release_external_artwork','track_external_lyrics','external_link',
     'release_group_change','release_group_default','release_issue_review','release_claim','revert',
     'artist_merge','artist_split','artist_rename','artist_candidate_ignore'));

ALTER TABLE curation_model_exceptions DROP CONSTRAINT curation_model_exceptions_tool_check;
ALTER TABLE curation_model_exceptions ADD CONSTRAINT curation_model_exceptions_tool_check
    CHECK (tool IN ('resolve_release_match','correct_release'));

ALTER TABLE tracks DROP COLUMN edit_revision;
