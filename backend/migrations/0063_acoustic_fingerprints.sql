CREATE TABLE track_acoustic_fingerprints (
    track_id            UUID PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
    pipeline_version    SMALLINT NOT NULL CHECK (pipeline_version > 0),
    content_fingerprint TEXT,
    source_size         BIGINT,
    source_modified_at  TIMESTAMPTZ,
    cue_index_frames    INTEGER,
    cue_end_frames      INTEGER,
    status              TEXT NOT NULL CHECK (status IN ('done','failed')),
    fingerprint         TEXT CHECK (octet_length(fingerprint) BETWEEN 1 AND 65536),
    duration_seconds    INTEGER CHECK (duration_seconds > 0),
    failure_category    TEXT CHECK (failure_category IN ('too_short','decode_failed','timeout')),
    attempts            SMALLINT NOT NULL DEFAULT 1 CHECK (attempts > 0),
    computed_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT track_acoustic_fingerprints_status_ck CHECK (
        (status = 'done' AND fingerprint IS NOT NULL AND duration_seconds IS NOT NULL AND failure_category IS NULL) OR
        (status = 'failed' AND fingerprint IS NULL AND failure_category IS NOT NULL))
);

CREATE TABLE curation_source_keys (
    source TEXT NOT NULL CHECK (source ~ '^[a-z0-9._-]{1,64}$'),
    name TEXT NOT NULL CHECK (name ~ '^[a-z_]{1,32}$'),
    ciphertext BYTEA NOT NULL,
    last_four TEXT NOT NULL CHECK (char_length(last_four) <= 4),
    status TEXT NOT NULL DEFAULT 'unchecked' CHECK (status IN ('unchecked','valid','invalid')),
    updated_by UUID NOT NULL REFERENCES users(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source, name)
);
