ALTER TABLE scan_runs
    ADD COLUMN IF NOT EXISTS cancel_requested_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS scan_runs_running_index
    ON scan_runs(started_at, id)
    WHERE status = 'running';
