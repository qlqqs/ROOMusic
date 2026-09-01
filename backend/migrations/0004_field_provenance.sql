ALTER TABLE track_observations
    ADD COLUMN IF NOT EXISTS field_name TEXT,
    ADD COLUMN IF NOT EXISTS value TEXT;

CREATE INDEX IF NOT EXISTS track_observations_track_field_index
    ON track_observations(track_id, field_name, observed_at DESC);
