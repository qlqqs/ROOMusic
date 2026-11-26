ALTER TABLE releases DROP COLUMN curation_attempted_change_at;
ALTER TABLE scan_runs DROP COLUMN whole_library;
ALTER TABLE library_roots
    DROP COLUMN last_poll_started_at,
    DROP COLUMN watch_baseline_at,
    DROP COLUMN poll_interval_minutes;
