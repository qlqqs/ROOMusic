DROP TABLE curation_artist_credit_proposals;
DROP INDEX curation_unlisted_due_idx;
ALTER TABLE curation_pending_items DROP COLUMN route,
    DROP CONSTRAINT curation_pending_items_status_check,
    ADD CONSTRAINT curation_pending_items_status_check CHECK (status IN ('waiting_model','in_progress','resolved','waiting_human','shelved'));
ALTER TABLE curation_work_states DROP CONSTRAINT curation_work_states_status_check,
    ADD CONSTRAINT curation_work_states_status_check CHECK (status IN ('matched','uncertain','no_match','try_later','done'));
ALTER TABLE external_links DROP CONSTRAINT external_links_object_type_check,
    ADD CONSTRAINT external_links_object_type_check CHECK (object_type IN ('release','release_group','track','artist'));
