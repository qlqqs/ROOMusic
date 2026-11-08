ALTER TABLE track_analyses
    ADD COLUMN IF NOT EXISTS loudness_range_lu REAL CHECK (loudness_range_lu IS NULL OR loudness_range_lu >= 0),
    ADD COLUMN IF NOT EXISTS sample_peak_dbfs REAL;

CREATE OR REPLACE VIEW track_display_durations AS
SELECT t.id AS track_id,
       CASE WHEN a.status = 'done' AND a.duration_ms > 0
             AND a.content_fingerprint IS NOT DISTINCT FROM t.content_fingerprint
             AND a.source_size IS NOT DISTINCT FROM t.source_size
             AND a.source_modified_at IS NOT DISTINCT FROM t.source_modified_at
             AND a.cue_index_frames IS NOT DISTINCT FROM t.cue_index_frames
             AND a.cue_end_frames IS NOT DISTINCT FROM t.cue_end_frames
             AND (t.duration_seconds IS NULL
                  OR NOT (t.duration_seconds > 0 AND t.duration_seconds < 'Infinity'::float8)
                  OR ABS(a.duration_ms / 1000.0 - t.duration_seconds) > GREATEST(1.0, a.duration_ms / 100000.0))
            THEN a.duration_ms / 1000.0
            ELSE t.duration_seconds END::float8 AS duration_seconds
FROM tracks t LEFT JOIN track_analyses a ON a.track_id = t.id;
