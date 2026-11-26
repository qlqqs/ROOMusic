ALTER TABLE library_roots
    ADD COLUMN poll_interval_minutes smallint NOT NULL DEFAULT 60
        CHECK (poll_interval_minutes IN (15, 60, 240, 720, 1440)),
    ADD COLUMN watch_baseline_at timestamptz,
    ADD COLUMN last_poll_started_at timestamptz;
ALTER TABLE scan_runs ADD COLUMN whole_library boolean NOT NULL DEFAULT true;
ALTER TABLE releases ADD COLUMN curation_attempted_change_at timestamptz;
