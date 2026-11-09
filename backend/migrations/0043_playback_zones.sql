CREATE TABLE playback_devices (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind TEXT NOT NULL DEFAULT 'browser' CHECK (kind IN ('browser', 'native')),
    capabilities JSONB NOT NULL DEFAULT '{}',
    name TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    volume REAL NOT NULL DEFAULT 1 CHECK (volume >= 0 AND volume <= 1),
    muted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (id, user_id)
);
CREATE INDEX playback_devices_user_idx ON playback_devices (user_id);

CREATE TABLE playback_zones (
    id UUID PRIMARY KEY,
    user_id UUID NOT NULL,
    device_id UUID NOT NULL UNIQUE,
    mode TEXT NOT NULL DEFAULT 'sequential' CHECK (mode IN ('sequential', 'repeat_all', 'repeat_one', 'shuffle')),
    -- 当前条目由服务保证属于本区；转移会搬动条目，不设外键。
    current_entry_id UUID,
    position_seconds DOUBLE PRECISION NOT NULL DEFAULT 0 CHECK (position_seconds >= 0 AND position_seconds <= 10000000),
    position_saved_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    history UUID[] NOT NULL DEFAULT '{}',
    shuffle_bag UUID[] NOT NULL DEFAULT '{}',
    leveling TEXT NOT NULL DEFAULT 'auto' CHECK (leveling IN ('off', 'auto', 'track', 'album')),
    crossfade BOOLEAN NOT NULL DEFAULT FALSE,
    crossfade_seconds SMALLINT NOT NULL DEFAULT 5 CHECK (crossfade_seconds IN (3, 5, 8, 12)),
    gapless BOOLEAN NOT NULL DEFAULT TRUE,
    quality TEXT NOT NULL DEFAULT 'lossless' CHECK (quality IN ('lossless', 'lossy')),
    revision BIGINT NOT NULL DEFAULT 0,
    queue_revision BIGINT NOT NULL DEFAULT 0,
    imported_at TIMESTAMPTZ,
    FOREIGN KEY (device_id, user_id) REFERENCES playback_devices (id, user_id) ON DELETE CASCADE
);
CREATE INDEX playback_zones_user_idx ON playback_zones (user_id);

CREATE TABLE playback_queue_entries (
    zone_id UUID NOT NULL REFERENCES playback_zones(id) ON DELETE CASCADE,
    entry_id UUID NOT NULL,
    position INTEGER NOT NULL CHECK (position >= 0),
    track_id UUID NOT NULL REFERENCES tracks(id),
    PRIMARY KEY (zone_id, entry_id),
    UNIQUE (zone_id, position) DEFERRABLE INITIALLY DEFERRED
);
