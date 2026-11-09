ALTER TABLE playback_zones
    ADD COLUMN convolution JSONB NOT NULL DEFAULT '{"enabled":false,"ir_id":null,"before_peq":false}';
