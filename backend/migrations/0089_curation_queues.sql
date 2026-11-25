ALTER TABLE curation_settings ADD COLUMN queue_baseline_at timestamptz;

ALTER TABLE curation_work_states
    DROP CONSTRAINT curation_work_states_status_check,
    DROP CONSTRAINT curation_work_states_subject_check,
    ADD CONSTRAINT curation_work_states_status_check CHECK
        (status IN ('pending','matched','uncertain','no_match','try_later','done','unlisted','no_usable_candidates')),
    ADD CONSTRAINT curation_work_states_subject_check CHECK
        (subject IN ('release','artwork','lyrics','artist_media','fingerprint')),
    ADD COLUMN needs_work boolean NOT NULL DEFAULT false,
    ADD COLUMN manual_requested_at timestamptz,
    ADD COLUMN queue_release_id uuid,
    ADD COLUMN queue_class text,
    ADD COLUMN queue_has_id boolean NOT NULL DEFAULT false,
    ADD COLUMN queue_gap integer[],
    ADD COLUMN queue_artist_unlinked boolean NOT NULL DEFAULT false,
    ADD COLUMN queue_added_at timestamptz,
    ADD COLUMN queue_identity text;

CREATE INDEX curation_work_queue_idx ON curation_work_states(subject, queue_release_id, target_id)
    WHERE needs_work;
