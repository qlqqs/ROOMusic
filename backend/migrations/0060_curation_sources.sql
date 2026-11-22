CREATE TABLE curation_source_settings (
    source TEXT PRIMARY KEY CHECK (source ~ '^[a-z0-9._-]{1,64}$'),
    enabled BOOLEAN NOT NULL,
    updated_by UUID NOT NULL REFERENCES users(id),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE external_links (
    id UUID PRIMARY KEY,
    source TEXT NOT NULL CHECK (source ~ '^[a-z0-9._-]{1,64}$'),
    object_type TEXT NOT NULL CHECK (object_type IN ('release','release_group','track','artist')),
    object_id UUID NOT NULL,
    external_id TEXT NOT NULL CHECK (length(external_id) BETWEEN 1 AND 128),
    method TEXT NOT NULL CHECK (method ~ '^[a-z0-9_]{1,32}$'),
    confidence TEXT NOT NULL CHECK (confidence ~ '^[a-z0-9_]{1,32}$'),
    operation_id UUID REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (source, object_type, object_id)
);
CREATE INDEX external_links_external_idx ON external_links (source, object_type, external_id);
