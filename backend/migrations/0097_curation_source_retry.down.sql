ALTER TABLE curation_pending_items
    DROP COLUMN claim_source_recovery_at,
    DROP COLUMN reset_item_usage_on_claim,
    DROP COLUMN source_retry_count;
