DELETE FROM curation_work_states WHERE subject='fingerprint' OR status='pending';
DROP INDEX curation_work_queue_idx;
ALTER TABLE curation_work_states
    DROP CONSTRAINT curation_work_states_status_check,
    DROP CONSTRAINT curation_work_states_subject_check,
    ADD CONSTRAINT curation_work_states_status_check CHECK
        (status IN ('matched','uncertain','no_match','try_later','done','unlisted','no_usable_candidates')),
    ADD CONSTRAINT curation_work_states_subject_check CHECK
        (subject IN ('release','artwork','lyrics','artist_media')),
    DROP COLUMN needs_work,
    DROP COLUMN manual_requested_at,
    DROP COLUMN queue_release_id,
    DROP COLUMN queue_class,
    DROP COLUMN queue_has_id,
    DROP COLUMN queue_gap,
    DROP COLUMN queue_artist_unlinked,
    DROP COLUMN queue_added_at,
    DROP COLUMN queue_identity;
ALTER TABLE curation_settings DROP COLUMN queue_baseline_at;
