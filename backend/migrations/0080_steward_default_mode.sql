ALTER TABLE steward_user_settings ADD COLUMN default_mode text NOT NULL DEFAULT 'assistant'
    CHECK (default_mode IN ('assistant','steward','operator'));
