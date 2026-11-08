DROP VIEW IF EXISTS track_display_durations;
ALTER TABLE track_analyses
    DROP COLUMN IF EXISTS sample_peak_dbfs,
    DROP COLUMN IF EXISTS loudness_range_lu;
