CREATE TABLE dsp_ir_files (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    file_name text NOT NULL CHECK (char_length(file_name) BETWEEN 1 AND 255),
    container text NOT NULL CHECK (container IN ('zip', 'wav', 'flac', 'aiff', 'm4a')),
    sample_rate integer NOT NULL CHECK (sample_rate > 0),
    channels integer NOT NULL CHECK (channels BETWEEN 1 AND 8),
    paths integer NOT NULL CHECK (paths BETWEEN 1 AND 32),
    taps integer NOT NULL CHECK (taps BETWEEN 1 AND 524288),
    size_bytes integer NOT NULL CHECK (size_bytes BETWEEN 1 AND 33554432),
    sha256 bytea NOT NULL CHECK (octet_length(sha256) = 32),
    content bytea NOT NULL CHECK (octet_length(content) = size_bytes),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, user_id)
);
CREATE INDEX dsp_ir_files_user_id_idx ON dsp_ir_files(user_id, created_at, id);
