ALTER TABLE external_links DROP CONSTRAINT external_links_object_type_check,
    ADD CONSTRAINT external_links_object_type_check CHECK (object_type IN ('release','release_group','track','artist','release_group_match'));

ALTER TABLE curation_work_states DROP CONSTRAINT curation_work_states_status_check,
    ADD CONSTRAINT curation_work_states_status_check CHECK (status IN ('matched','uncertain','no_match','try_later','done','unlisted','no_usable_candidates'));
CREATE INDEX curation_unlisted_due_idx ON curation_work_states(next_attempt_at)
    WHERE status='unlisted' AND next_attempt_at IS NOT NULL;

ALTER TABLE curation_pending_items DROP CONSTRAINT curation_pending_items_status_check,
    ADD CONSTRAINT curation_pending_items_status_check CHECK (status IN ('waiting_model','in_progress','resolved','waiting_human','shelved','waiting_route')),
    ADD COLUMN route text CHECK (route IN ('select','lookup','web'));

CREATE TABLE curation_artist_credit_proposals (
    id uuid PRIMARY KEY,
    release_id uuid NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    identity_digest text NOT NULL,
    operation_id uuid NOT NULL REFERENCES operations(id),
    evidence jsonb NOT NULL,
    status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','accepted','declined','stale'))
);
CREATE INDEX curation_artist_credit_proposals_release_idx ON curation_artist_credit_proposals(release_id,status);
