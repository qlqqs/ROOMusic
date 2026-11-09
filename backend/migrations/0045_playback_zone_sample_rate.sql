ALTER TABLE playback_zones
    ADD COLUMN sample_rate JSONB NOT NULL DEFAULT '{"strategy":"none","custom_rate":null,"filter":"linear_precise"}';
