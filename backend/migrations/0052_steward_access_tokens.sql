CREATE TABLE steward_access_tokens (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    scope text NOT NULL CHECK (scope IN ('read', 'read_write')),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    token_tail text NOT NULL CHECK (char_length(token_tail) = 4),
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz,
    last_used_at timestamptz,
    revoked_at timestamptz
);
CREATE INDEX steward_access_tokens_user_idx ON steward_access_tokens (user_id);
