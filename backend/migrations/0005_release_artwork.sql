CREATE TABLE IF NOT EXISTS release_artworks (
    release_id UUID PRIMARY KEY REFERENCES releases(id) ON DELETE CASCADE,
    content_hash TEXT NOT NULL,
    mime_type TEXT NOT NULL CHECK (mime_type IN ('image/jpeg','image/png','image/gif','image/webp')),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    storage_key TEXT NOT NULL UNIQUE,
    source_type TEXT NOT NULL CHECK (source_type IN ('embedded','folder')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
