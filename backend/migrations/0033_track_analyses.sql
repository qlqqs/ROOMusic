CREATE TABLE IF NOT EXISTS track_analyses (
    track_id            UUID PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
    analyzer_version    SMALLINT    NOT NULL CHECK (analyzer_version > 0),
    content_fingerprint TEXT,
    source_size         BIGINT,
    source_modified_at  TIMESTAMPTZ,
    cue_index_frames    INTEGER,
    cue_end_frames      INTEGER,
    status              TEXT        NOT NULL CHECK (status IN ('done','failed')),
    integrated_lufs     REAL,
    true_peak_dbtp      REAL,
    duration_ms         INTEGER CHECK (duration_ms IS NULL OR duration_ms >= 0),
    leading_silence_ms  INTEGER CHECK (leading_silence_ms IS NULL OR leading_silence_ms >= 0),
    trailing_silence_ms INTEGER CHECK (trailing_silence_ms IS NULL OR trailing_silence_ms >= 0),
    waveform            BYTEA CHECK (waveform IS NULL OR octet_length(waveform) BETWEEN 1 AND 1000),
    failure_category    TEXT CHECK (failure_category IS NULL OR failure_category IN
                          ('source_unavailable','source_changed','ffmpeg_exit','parse','timeout')),
    attempts            SMALLINT    NOT NULL DEFAULT 1 CHECK (attempts > 0),
    analyzed_at         TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT track_analyses_status_ck CHECK (
        (status = 'done'   AND failure_category IS NULL AND waveform IS NOT NULL AND duration_ms IS NOT NULL) OR
        (status = 'failed' AND failure_category IS NOT NULL AND waveform IS NULL))
);
