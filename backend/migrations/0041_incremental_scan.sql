CREATE TABLE library_parse_cache (
    root_id UUID NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    relative_path TEXT NOT NULL,
    directory TEXT NOT NULL,
    source_size BIGINT NOT NULL CHECK (source_size >= 0),
    source_modified_at TIMESTAMPTZ NOT NULL,
    parser_version TEXT NOT NULL CHECK (parser_version <> ''),
    observation JSONB NOT NULL,
    seen_scan_id UUID,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (root_id, relative_path)
);
CREATE INDEX library_parse_cache_directory_index ON library_parse_cache(root_id, directory);
CREATE TABLE scan_candidate_states (
    root_id UUID NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    candidate_key TEXT NOT NULL,
    release_id UUID NOT NULL,
    signature TEXT NOT NULL CHECK (signature ~ '^[0-9a-f]{64}$'),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (root_id, candidate_key)
);
ALTER TABLE scan_runs ADD COLUMN scan_mode TEXT NOT NULL DEFAULT 'incremental'
    CHECK (scan_mode IN ('incremental', 'full'));
