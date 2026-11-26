DROP INDEX curation_local_member_idx;
DROP INDEX curation_local_decision_files_idx;
DROP TABLE local_model_groupings;
DROP TABLE local_credit_parts;
DELETE FROM curation_suppressions WHERE subject IN ('local_model_grouping','credit_parts_album_artist','credit_parts_artist');
DELETE FROM operations WHERE reverts_operation_id IN (SELECT id FROM operations WHERE operation_type='local_model_grouping');
DELETE FROM operations WHERE operation_type='local_model_grouping';
UPDATE curation_pending_items SET status='resolved',resolution='inputs_changed',resolved_at=now(),updated_at=now()
WHERE kind='local_cleanup' AND rule_version=4 AND status<>'resolved';
ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK (operation_type IN
    ('release_correction','release_auto_correction','track_correction','track_auto_correction','release_external_artwork','track_external_lyrics','external_link',
     'release_group_change','release_group_default','release_issue_review','release_claim','revert',
     'artist_merge','artist_split','artist_rename','artist_candidate_ignore','release_merge','release_auto_merge','artist_external_artwork','artist_external_bio',
     'artist_manual_image','artist_manual_bio','artist_link'));
