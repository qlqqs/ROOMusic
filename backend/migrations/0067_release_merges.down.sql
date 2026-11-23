-- 还原仍在合并中的碟。合并转移过的收藏保留回退时的状态。
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM media m JOIN release_merges r ON r.source_release_id=m.release_id) THEN
        RAISE EXCEPTION '发行合并来源仍有自有碟';
    END IF;
END $$;

UPDATE media SET release_id=origin_release_id, position=origin_position,
    origin_release_id=NULL, origin_position=NULL
WHERE origin_release_id IS NOT NULL;
DELETE FROM release_merges;

WITH merge_operations AS (
    SELECT id FROM operations WHERE operation_type IN ('release_merge','release_auto_merge')
)
UPDATE steward_plan_items SET operation_id=NULL
WHERE operation_id IN (SELECT id FROM merge_operations
    UNION ALL SELECT id FROM operations WHERE reverts_operation_id IN (SELECT id FROM merge_operations));

DELETE FROM curation_model_exceptions WHERE tool='merge_releases';
DELETE FROM curation_pending_items WHERE kind='release_merge';
DELETE FROM curation_suppressions WHERE target_type='release' AND subject='release_merge';
DELETE FROM operations WHERE reverts_operation_id IN (
    SELECT id FROM operations WHERE operation_type IN ('release_merge','release_auto_merge')
);
DELETE FROM operations WHERE operation_type IN ('release_merge','release_auto_merge');

SET CONSTRAINTS ALL IMMEDIATE;
DROP TABLE operations_release_targets;
ALTER TABLE media DROP COLUMN origin_release_id, DROP COLUMN origin_position;
DROP TABLE release_merges;

ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK (operation_type IN
    ('release_correction','release_auto_correction','track_correction','track_auto_correction','release_external_artwork','track_external_lyrics','external_link',
     'release_group_change','release_group_default','release_issue_review','release_claim','revert',
     'artist_merge','artist_split','artist_rename','artist_candidate_ignore'));

ALTER TABLE curation_pending_items DROP CONSTRAINT curation_pending_items_kind_check;
ALTER TABLE curation_pending_items ADD CONSTRAINT curation_pending_items_kind_check
    CHECK (kind IN ('release_match','recording_match'));

ALTER TABLE curation_model_exceptions DROP CONSTRAINT curation_model_exceptions_tool_check;
ALTER TABLE curation_model_exceptions ADD CONSTRAINT curation_model_exceptions_tool_check
    CHECK (tool IN ('resolve_release_match','correct_release','correct_track'));
