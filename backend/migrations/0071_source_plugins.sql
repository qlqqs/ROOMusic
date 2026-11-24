CREATE TABLE source_plugins (
    id text PRIMARY KEY CHECK (id ~ '^[a-z0-9-]{3,40}$'),
    manifest jsonb NOT NULL,
    capabilities jsonb NOT NULL,
    sha256 text NOT NULL,
    wasm_hash text NOT NULL,
    manifest_hash text NOT NULL,
    installed_at timestamptz NOT NULL DEFAULT now(),
    disabled_reason text NOT NULL DEFAULT '' CHECK (disabled_reason IN ('','hash_changed'))
);
ALTER TABLE curation_source_settings DROP CONSTRAINT curation_source_settings_source_check;
ALTER TABLE curation_source_settings ADD CONSTRAINT curation_source_settings_source_check CHECK (source ~ '^[a-z0-9._-]{1,64}$' OR source ~ '^plugin:[a-z0-9-]{3,40}$');
ALTER TABLE curation_source_keys DROP CONSTRAINT curation_source_keys_source_check;
ALTER TABLE curation_source_keys ADD CONSTRAINT curation_source_keys_source_check CHECK (source ~ '^[a-z0-9._-]{1,64}$' OR source ~ '^plugin:[a-z0-9-]{3,40}$');
ALTER TABLE track_external_lyrics DROP CONSTRAINT track_external_lyrics_source_check;
ALTER TABLE track_external_lyrics ADD CONSTRAINT track_external_lyrics_source_check CHECK (source ~ '^[a-z0-9._-]{1,64}$' OR source ~ '^plugin:[a-z0-9-]{3,40}$');
ALTER TABLE release_external_artworks DROP CONSTRAINT release_external_artworks_source_check;
ALTER TABLE release_external_artworks ADD CONSTRAINT release_external_artworks_source_check CHECK (source ~ '^[a-z0-9._-]{1,64}$' OR source ~ '^plugin:[a-z0-9-]{3,40}$');
ALTER TABLE artist_external_artworks DROP CONSTRAINT artist_external_artworks_source_check;
ALTER TABLE artist_external_artworks ADD CONSTRAINT artist_external_artworks_source_check CHECK (source IN ('fanarttv','theaudiodb','commons') OR source ~ '^plugin:[a-z0-9-]{3,40}$');
ALTER TABLE artist_external_bios DROP CONSTRAINT artist_external_bios_source_check;
ALTER TABLE artist_external_bios ADD CONSTRAINT artist_external_bios_source_check CHECK (source IN ('wikipedia','theaudiodb') OR source ~ '^plugin:[a-z0-9-]{3,40}$');
