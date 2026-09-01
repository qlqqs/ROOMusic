-- Core 0 的首个迁移仅建立迁移可用性记录；业务表由对应纵向切片添加。
CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO schema_migrations (version)
VALUES (1)
ON CONFLICT (version) DO NOTHING;
