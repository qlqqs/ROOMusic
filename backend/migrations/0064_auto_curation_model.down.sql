DROP TABLE curation_model_exceptions, curation_model_usage;

UPDATE curation_pending_items SET status = 'waiting_model' WHERE status IN ('in_progress', 'waiting_human');
DROP INDEX curation_pending_items_due_idx;
ALTER TABLE curation_pending_items
    DROP CONSTRAINT curation_pending_items_claim_ck,
    DROP COLUMN conversation_id, DROP COLUMN claimed_at, DROP COLUMN attempts,
    DROP COLUMN review_retries, DROP COLUMN next_attempt_at, DROP COLUMN shelved_reason;

UPDATE steward_plan_items SET decision = 'declined' WHERE decision = 'escalated';
ALTER TABLE steward_plan_items DROP CONSTRAINT steward_plan_items_decision_check,
    ADD CONSTRAINT steward_plan_items_decision_check CHECK (decision IN ('pending','accepted','declined','rejected_by_review'));
ALTER TABLE steward_plans DROP COLUMN background;

DELETE FROM steward_conversations WHERE kind = 'curation';
DROP INDEX steward_conversations_curation_idx;
ALTER TABLE steward_conversations DROP CONSTRAINT steward_conversations_curation_mode_ck, DROP COLUMN kind;
ALTER TABLE curation_settings DROP COLUMN model, DROP COLUMN model_daily_token_limit, DROP COLUMN model_updated_at;
