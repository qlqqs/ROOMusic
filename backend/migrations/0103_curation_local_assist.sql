CREATE INDEX curation_local_decision_files_idx ON curation_pending_items(identity_digest)
    WHERE kind='local_cleanup' AND status='resolved' AND resolution IN ('local_cleanup','undetermined');
CREATE INDEX curation_local_member_idx ON curation_pending_items USING gin ((evidence->'release_ids'))
    WHERE kind='local_cleanup';
CREATE TABLE local_credit_parts (
    operation_id uuid NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    target_id uuid NOT NULL, role text NOT NULL, source_ref text NOT NULL,
    raw text NOT NULL, parts jsonb NOT NULL, joins jsonb NOT NULL,
    PRIMARY KEY (operation_id, target_id, role, source_ref)
);
CREATE TABLE local_model_groupings (
    root_id uuid NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    directory_key text NOT NULL, operation_id uuid UNIQUE NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    source_digest text NOT NULL, directories text[] NOT NULL, assignments jsonb NOT NULL, other_paths text[] NOT NULL,
    PRIMARY KEY (root_id, directory_key)
);
UPDATE curation_pending_items SET status='resolved', resolution='inputs_changed', resolved_at=now(),updated_at=now()
WHERE kind='local_cleanup' AND status IN ('waiting_model','waiting_route','in_progress');
ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK (operation_type IN
    ('release_correction','release_auto_correction','track_correction','track_auto_correction','release_external_artwork','track_external_lyrics','external_link',
     'release_group_change','release_group_default','release_issue_review','release_claim','revert',
     'artist_merge','artist_split','artist_rename','artist_candidate_ignore','release_merge','release_auto_merge','artist_external_artwork','artist_external_bio',
     'artist_manual_image','artist_manual_bio','artist_link','local_model_grouping'));
