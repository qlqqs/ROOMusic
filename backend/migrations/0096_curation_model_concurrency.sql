ALTER TABLE curation_settings ADD COLUMN model_concurrency integer NOT NULL DEFAULT 4
    CHECK (model_concurrency BETWEEN 1 AND 16);
