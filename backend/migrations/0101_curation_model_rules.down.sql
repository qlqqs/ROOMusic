DROP TABLE curation_model_state;
ALTER TABLE curation_pending_items DROP COLUMN empty_count, DROP COLUMN model_stop_code, DROP COLUMN single_request;
ALTER TABLE curation_settings DROP COLUMN max_request_tokens;
-- 恢复 0091 与 0096 的列顺序和管理员的并发设置。
ALTER TABLE curation_settings RENAME COLUMN model_concurrency TO saved_concurrency;
ALTER TABLE curation_settings ALTER COLUMN saved_concurrency DROP NOT NULL,
    DROP CONSTRAINT curation_settings_model_concurrency_check;
ALTER TABLE curation_settings ADD COLUMN web_daily_token_limit bigint CHECK (web_daily_token_limit > 0),
    ADD COLUMN model_concurrency integer NOT NULL DEFAULT 4 CHECK (model_concurrency BETWEEN 1 AND 16);
UPDATE curation_settings SET model_concurrency=saved_concurrency;
ALTER TABLE curation_settings DROP COLUMN saved_concurrency;
ALTER TABLE curation_model_usage
    ADD COLUMN web_input_tokens bigint NOT NULL DEFAULT 0 CHECK (web_input_tokens >= 0),
    ADD COLUMN web_output_tokens bigint NOT NULL DEFAULT 0 CHECK (web_output_tokens >= 0);
