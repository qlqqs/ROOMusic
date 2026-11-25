CREATE TABLE subsonic_settings (
    id smallint PRIMARY KEY CHECK (id = 1),
    subsonic_enabled boolean NOT NULL DEFAULT true
);
INSERT INTO subsonic_settings (id) VALUES (1);

CREATE TABLE subsonic_credentials (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    sealed bytea NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- 命令行恢复密码只连接数据库。所有改密入口都先撤销旧副本，
-- 知道明文且持有实例密钥的入口会在同一事务内写入新副本。
CREATE FUNCTION invalidate_subsonic_password() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM id FROM subsonic_settings WHERE id = 1 FOR SHARE;
    DELETE FROM subsonic_credentials WHERE user_id = NEW.id;
    RETURN NEW;
END;
$$;
CREATE TRIGGER invalidate_subsonic_password AFTER UPDATE OF password_hash ON users
    FOR EACH ROW WHEN (OLD.password_hash IS DISTINCT FROM NEW.password_hash)
    EXECUTE FUNCTION invalidate_subsonic_password();
