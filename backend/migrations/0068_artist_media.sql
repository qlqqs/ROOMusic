ALTER TABLE artists ADD COLUMN auto_revision BIGINT NOT NULL DEFAULT 1;

CREATE TABLE artist_external_artworks (
    artist_id UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    storage_key TEXT NOT NULL,
    mime TEXT NOT NULL CHECK (mime IN ('image/jpeg','image/png','image/gif','image/webp')),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    source TEXT NOT NULL CHECK (source IN ('fanarttv','theaudiodb','commons')),
    source_url TEXT NOT NULL CHECK (source_url <> ''),
    author TEXT,
    license TEXT,
    license_url TEXT,
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (source <> 'commons' OR COALESCE(btrim(license),'') <> '')
);
CREATE INDEX artist_external_artworks_storage_key_idx ON artist_external_artworks(storage_key);

CREATE TABLE artist_external_bios (
    artist_id UUID PRIMARY KEY REFERENCES artists(id) ON DELETE CASCADE,
    text TEXT NOT NULL CHECK (char_length(text) BETWEEN 1 AND 4000),
    lang TEXT NOT NULL CHECK (lang IN ('zh','en')),
    source TEXT NOT NULL CHECK (source IN ('wikipedia','theaudiodb')),
    source_url TEXT NOT NULL CHECK (source_url <> ''),
    license TEXT,
    license_url TEXT,
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK (operation_type IN
    ('release_correction','release_auto_correction','track_correction','track_auto_correction','release_external_artwork','track_external_lyrics','external_link',
     'release_group_change','release_group_default','release_issue_review','release_claim','revert',
     'artist_merge','artist_split','artist_rename','artist_candidate_ignore','release_merge','release_auto_merge','artist_external_artwork','artist_external_bio'));

ALTER TABLE curation_work_states DROP CONSTRAINT curation_work_states_subject_check;
ALTER TABLE curation_work_states ADD CONSTRAINT curation_work_states_subject_check
    CHECK (subject IN ('release','artwork','lyrics','artist_media'));
