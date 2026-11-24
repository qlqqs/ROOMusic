DROP TABLE artwork_retained, artist_manual_artworks, artist_manual_bios;

CREATE TEMP TABLE artist_manual_operation_ids ON COMMIT DROP AS
    SELECT id FROM operations WHERE operation_type IN ('artist_manual_image','artist_manual_bio','artist_link')
    UNION ALL SELECT r.id FROM operations r JOIN operations op ON op.id=r.reverts_operation_id
    WHERE op.operation_type IN ('artist_manual_image','artist_manual_bio','artist_link');

UPDATE steward_plan_items SET operation_id=NULL WHERE operation_id IN (SELECT id FROM artist_manual_operation_ids);
DELETE FROM curation_suppressions WHERE operation_id IN (SELECT id FROM artist_manual_operation_ids);
DELETE FROM external_links WHERE operation_id IN (SELECT id FROM artist_manual_operation_ids);
DELETE FROM operations WHERE id IN (SELECT id FROM artist_manual_operation_ids);
DROP TABLE artist_manual_operation_ids;

SET CONSTRAINTS ALL IMMEDIATE;
ALTER TABLE artist_external_artworks DROP COLUMN basis_source, DROP COLUMN basis_id;
ALTER TABLE artist_external_bios DROP COLUMN basis_source, DROP COLUMN basis_id;
ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK (operation_type IN
    ('release_correction','release_auto_correction','track_correction','track_auto_correction','release_external_artwork','track_external_lyrics','external_link',
     'release_group_change','release_group_default','release_issue_review','release_claim','revert',
     'artist_merge','artist_split','artist_rename','artist_candidate_ignore','release_merge','release_auto_merge','artist_external_artwork','artist_external_bio'));
