ALTER TABLE curation_pending_items
    DROP COLUMN match_rule_version,
    DROP COLUMN identity_digest;

ALTER TABLE curation_work_states
    DROP COLUMN local_rule_version,
    DROP COLUMN match_rule_version,
    DROP COLUMN identity_digest,
    DROP COLUMN gap_digest,
    DROP COLUMN requery_match_version;
