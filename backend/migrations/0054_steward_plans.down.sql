DROP TABLE steward_plan_items;
DROP INDEX steward_plans_conversation_idx;
DROP TABLE steward_plans;
UPDATE steward_tool_calls SET status='failed',error_code='not_executed' WHERE status='awaiting_confirmation';
ALTER TABLE steward_tool_calls DROP CONSTRAINT steward_tool_calls_status_check;
ALTER TABLE steward_tool_calls ADD CONSTRAINT steward_tool_calls_status_check CHECK (status IN ('running','succeeded','failed'));
ALTER TABLE steward_tool_calls DROP COLUMN idempotency_key;
ALTER TABLE steward_conversations DROP COLUMN auto_approved_tools;
ALTER TABLE operations DROP COLUMN origin;
