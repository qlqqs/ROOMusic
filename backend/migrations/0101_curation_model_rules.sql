ALTER TABLE curation_settings
    DROP COLUMN web_daily_token_limit,
    ADD COLUMN max_request_tokens bigint NOT NULL DEFAULT 128000 CHECK (max_request_tokens BETWEEN 1 AND 1000000000000);
ALTER TABLE curation_model_usage DROP COLUMN web_input_tokens, DROP COLUMN web_output_tokens;

ALTER TABLE curation_pending_items
    ADD COLUMN empty_count smallint NOT NULL DEFAULT 0 CHECK (empty_count BETWEEN 0 AND 3),
    ADD COLUMN model_stop_code text,
    ADD COLUMN single_request boolean NOT NULL DEFAULT false;

UPDATE curation_pending_items SET attempts=0 WHERE attempts<>0 AND status IN ('waiting_model','waiting_route','in_progress');
UPDATE curation_pending_items SET next_attempt_at=NULL WHERE reset_item_usage_on_claim AND status='waiting_route';

CREATE TABLE curation_model_state (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    config_key text NOT NULL DEFAULT '',
    model_key text NOT NULL DEFAULT '',
    pause_kind text,
    reason_code text,
    reason_detail text,
    retry_at timestamptz,
    backoff_seconds integer NOT NULL DEFAULT 0,
    streak_key text NOT NULL DEFAULT '',
    streak_items uuid[] NOT NULL DEFAULT '{}',
    tools_verified boolean NOT NULL DEFAULT false
);
INSERT INTO curation_model_state DEFAULT VALUES;
