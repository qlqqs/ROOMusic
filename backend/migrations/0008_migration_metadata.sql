-- 迁移执行器治理：为已应用版本保存不可变文件名与原始字节 SHA-256。
-- 列先允许 NULL，由执行器在同一事务中为历史版本补齐元数据。
ALTER TABLE schema_migrations
    ADD COLUMN IF NOT EXISTS name TEXT,
    ADD COLUMN IF NOT EXISTS checksum TEXT;
