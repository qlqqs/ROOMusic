ALTER TABLE library_roots ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled'));
ALTER TABLE library_roots ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0);
ALTER TABLE library_roots ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP;
CREATE TABLE IF NOT EXISTS library_root_operations (
    id UUID PRIMARY KEY,
    root_id UUID REFERENCES library_roots(id) ON DELETE SET NULL,
    actor_id UUID NOT NULL REFERENCES users(id),
    operation_type TEXT NOT NULL CHECK (operation_type IN ('create','disable','restore')),
    status TEXT NOT NULL CHECK (status IN ('succeeded','failed')),
    idempotency_key TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    expected_revision BIGINT,
    result_revision BIGINT,
    before_state JSONB NOT NULL DEFAULT '{}'::jsonb,
    after_state JSONB NOT NULL DEFAULT '{}'::jsonb,
    response JSONB NOT NULL DEFAULT '{}'::jsonb,
    error_code TEXT,
    request_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(actor_id, operation_type, idempotency_key)
);
CREATE INDEX IF NOT EXISTS library_root_operations_created_index ON library_root_operations(created_at DESC);
CREATE INDEX IF NOT EXISTS library_root_operations_root_index ON library_root_operations(root_id, created_at DESC);
INSERT INTO schema_migrations(version) VALUES (7) ON CONFLICT (version) DO NOTHING;
