ALTER TABLE playback_zones
    ADD COLUMN peq JSONB NOT NULL DEFAULT '{"enabled":false,"preamp_db":0,"bands":[]}';

CREATE TABLE dsp_peq_presets (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    peq JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);
