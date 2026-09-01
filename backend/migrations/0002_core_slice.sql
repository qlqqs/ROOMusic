CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS setup_state (
    id BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    completed_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS library_roots (
    id UUID PRIMARY KEY,
    path TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS scan_runs (
    id UUID PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('running','succeeded','failed','canceled','incomplete')),
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    error_message TEXT
);

CREATE TABLE IF NOT EXISTS releases (
    id UUID PRIMARY KEY,
    group_id UUID NOT NULL,
    title TEXT NOT NULL,
    artist TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS media (
    id UUID PRIMARY KEY,
    release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    title TEXT NOT NULL DEFAULT 'Medium'
);

CREATE TABLE IF NOT EXISTS tracks (
    id UUID PRIMARY KEY,
    medium_id UUID NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    position INTEGER NOT NULL,
    title TEXT NOT NULL,
    artist TEXT NOT NULL,
    disc_number INTEGER NOT NULL DEFAULT 1,
    source_root_id UUID NOT NULL REFERENCES library_roots(id),
    relative_path TEXT NOT NULL,
    source_status TEXT NOT NULL DEFAULT 'present' CHECK (source_status IN ('present','missing')),
    observed_at TIMESTAMPTZ NOT NULL,
    UNIQUE(source_root_id, relative_path)
);

CREATE TABLE IF NOT EXISTS scan_diagnostics (
    id BIGSERIAL PRIMARY KEY,
    scan_run_id UUID NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE,
    root_id UUID REFERENCES library_roots(id),
    relative_path TEXT,
    kind TEXT NOT NULL,
    message TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS tracks_release_index ON tracks(medium_id, position);
CREATE INDEX IF NOT EXISTS scan_diagnostics_run_index ON scan_diagnostics(scan_run_id, id);
