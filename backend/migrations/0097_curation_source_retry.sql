ALTER TABLE curation_pending_items
    ADD COLUMN source_retry_count integer NOT NULL DEFAULT 0 CHECK (source_retry_count >= 0),
    ADD COLUMN claim_source_recovery_at timestamptz,
    ADD COLUMN reset_item_usage_on_claim boolean NOT NULL DEFAULT false;
