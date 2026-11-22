CREATE TABLE release_external_artworks (
    release_id UUID PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,
    content_hash TEXT NOT NULL,
    mime_type TEXT NOT NULL CHECK (mime_type IN ('image/jpeg','image/png','image/gif','image/webp')),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    storage_key TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source ~ '^[a-z0-9._-]{1,64}$'),
    external_id TEXT NOT NULL CHECK (length(external_id) BETWEEN 1 AND 128),
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX release_external_artworks_storage_key_idx ON release_external_artworks (storage_key);

CREATE TABLE track_external_lyrics (
    track_id UUID PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
    lyrics TEXT NOT NULL CHECK (lyrics <> '' AND octet_length(lyrics) <= 65536),
    content_hash TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source ~ '^[a-z0-9._-]{1,64}$'),
    external_id TEXT NOT NULL CHECK (length(external_id) BETWEEN 1 AND 128),
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck CHECK(operation_type IN
 ('release_correction','release_auto_correction','release_external_artwork','track_external_lyrics','release_group_change','release_group_default','release_issue_review','release_claim','revert',
 'artist_merge','artist_split','artist_rename','artist_candidate_ignore'));
