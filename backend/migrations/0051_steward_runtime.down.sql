-- 回退删除管家设置、放行名单和对话数据。
DROP TABLE steward_tool_calls;
DROP TABLE steward_messages;
DROP TABLE steward_conversations;
DROP TABLE steward_egress_allowlist;
DROP TABLE steward_instance_settings;
DROP TABLE steward_user_settings;
