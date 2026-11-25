ALTER TABLE curation_work_states
    ADD COLUMN local_rule_version smallint,
    ADD COLUMN match_rule_version smallint,
    ADD COLUMN identity_digest text,
    ADD COLUMN gap_digest text,
    ADD COLUMN requery_match_version smallint;

ALTER TABLE curation_pending_items
    ADD COLUMN match_rule_version smallint,
    ADD COLUMN identity_digest text;
