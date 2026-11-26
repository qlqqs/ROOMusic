DELETE FROM curation_pending_items WHERE kind IN ('name_patrol','local_cleanup');
DROP INDEX curation_pending_items_open_idx;
CREATE UNIQUE INDEX curation_pending_items_open_idx ON curation_pending_items(target_type,target_id,kind)
    WHERE status <> 'resolved' AND kind NOT IN ('verify_artist_credit','verify_artist_merge','verify_name');
ALTER TABLE curation_pending_items DROP CONSTRAINT curation_pending_items_target_ck,
    ADD CONSTRAINT curation_pending_items_target_ck CHECK (
        (target_type='release' AND target_id IS NOT NULL AND artist_id IS NULL AND kind IN
            ('release_match','recording_match','release_merge','verify_artist_credit','verify_artist_merge','verify_name'))
        OR (target_type='artist' AND artist_id IS NOT NULL AND target_id IS NULL AND kind='artist_identity'));

ALTER TABLE curation_work_states DROP COLUMN patrol_revision;
