ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'admin' CHECK (role IN ('admin','user'));
ALTER TABLE users ADD COLUMN IF NOT EXISTS disabled_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS sessions_user_index ON sessions(user_id, revoked_at);
INSERT INTO schema_migrations(version) VALUES (6) ON CONFLICT (version) DO NOTHING;
