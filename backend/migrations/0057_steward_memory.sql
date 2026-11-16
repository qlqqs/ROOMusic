-- 已读游标与运行结束时间独立于会话标题、消息更新时间。
ALTER TABLE steward_conversations
    ADD COLUMN last_read_at timestamptz,
    ADD COLUMN last_run_finished_at timestamptz;

-- 记忆只属于本人，随成员删除；长度与空白校验由应用入口完成。
CREATE TABLE steward_memories (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    content text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 500),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX steward_memories_user_updated ON steward_memories(user_id, updated_at DESC, id);

-- 摘要不替代原始消息；分界只能由持有会话锁的运行写入。
ALTER TABLE steward_conversations
    ADD COLUMN summary text NOT NULL DEFAULT '',
    ADD COLUMN summary_through_message_id uuid,
    ADD COLUMN summary_updated_at timestamptz;
