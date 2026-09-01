CREATE TABLE IF NOT EXISTS release_groups (
    id UUID PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'releases_group_id_fkey'
          AND conrelid = 'releases'::regclass
    ) THEN
        ALTER TABLE releases
            ADD CONSTRAINT releases_group_id_fkey
            FOREIGN KEY (group_id) REFERENCES release_groups(id);
    END IF;
END
$$;

ALTER TABLE releases ADD COLUMN IF NOT EXISTS source_root_id UUID REFERENCES library_roots(id);
ALTER TABLE releases ADD COLUMN IF NOT EXISTS relative_directory TEXT;
CREATE UNIQUE INDEX IF NOT EXISTS releases_source_directory ON releases(source_root_id, relative_directory);

CREATE TABLE IF NOT EXISTS track_observations (
    id BIGSERIAL PRIMARY KEY,
    track_id UUID NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
    scan_run_id UUID NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE,
    source_kind TEXT NOT NULL,
    inferred BOOLEAN NOT NULL DEFAULT FALSE,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
