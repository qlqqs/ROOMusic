ALTER TABLE release_auto_values
    ADD COLUMN writer_level SMALLINT CHECK (writer_level BETWEEN 1 AND 4),
    ADD COLUMN scan_basis JSONB;

ALTER TABLE track_auto_values
    ADD COLUMN writer_level SMALLINT CHECK (writer_level BETWEEN 1 AND 4),
    ADD COLUMN scan_basis JSONB;
