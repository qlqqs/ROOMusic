CREATE TABLE curation_work_states (
    subject TEXT NOT NULL CHECK (subject IN ('release','artwork','lyrics')),
    target_id UUID NOT NULL,             -- release / artwork 是发行，lyrics 是曲目；不设外键（这些对象不被删除）
    rule_version SMALLINT NOT NULL,
    input_digest TEXT NOT NULL,          -- 十六进制 SHA-256
    status TEXT NOT NULL CHECK (status IN ('matched','uncertain','no_match','try_later','done')),
    attempts INTEGER NOT NULL DEFAULT 0, -- 得出结论的次数（try_later 不计）
    generation INTEGER NOT NULL DEFAULT 0, -- 代次：每次得出结论加一，幂等键由它派生（第四节）
    retry_count INTEGER NOT NULL DEFAULT 0, -- 连续 try_later 的次数，得出结论时清零
    next_attempt_at TIMESTAMPTZ,         -- 只有 try_later 有值
    result JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subject, target_id)
);
CREATE INDEX curation_work_states_progress_idx ON curation_work_states (subject, rule_version, status);
CREATE INDEX curation_work_states_due_idx ON curation_work_states (next_attempt_at) WHERE status = 'try_later';

CREATE TABLE curation_pending_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    target_type TEXT NOT NULL CHECK (target_type = 'release'),
    target_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('release_match','recording_match')),
    status TEXT NOT NULL CHECK (status IN ('waiting_model','in_progress','resolved','waiting_human','shelved')),
    rule_version SMALLINT NOT NULL,
    input_digest TEXT NOT NULL,
    evidence JSONB NOT NULL,
    candidates JSONB NOT NULL DEFAULT '[]',
    conflicts JSONB NOT NULL DEFAULT '[]',
    resolution TEXT CHECK (resolution ~ '^[a-z_]{1,32}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);
CREATE UNIQUE INDEX curation_pending_items_open_idx ON curation_pending_items (target_type, target_id, kind) WHERE status <> 'resolved';
CREATE INDEX curation_pending_items_status_idx ON curation_pending_items (status, created_at);

ALTER TABLE curation_settings ADD COLUMN paused BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK(operation_type IN
 ('release_correction','release_auto_correction','release_external_artwork','track_external_lyrics','external_link','release_group_change','release_group_default','release_issue_review','release_claim','revert',
 'artist_merge','artist_split','artist_rename','artist_candidate_ignore'));
