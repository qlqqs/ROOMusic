ALTER TABLE playback_zones
    ADD COLUMN downmix JSONB NOT NULL DEFAULT '{"enabled":true}';
