ALTER TABLE playback_zones
    ADD COLUMN headroom JSONB NOT NULL DEFAULT '{"enabled":false,"db":-3.0,"clip_indicator":false}';
