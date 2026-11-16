ALTER TABLE steward_conversations
    DROP COLUMN last_run_finished_at,
    DROP COLUMN last_read_at;

ALTER TABLE steward_conversations
    DROP COLUMN summary_updated_at,
    DROP COLUMN summary_through_message_id,
    DROP COLUMN summary;
DROP TABLE steward_memories;
