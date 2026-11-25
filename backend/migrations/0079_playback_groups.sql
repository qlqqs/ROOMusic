CREATE TABLE playback_group_members (
    device_id uuid PRIMARY KEY,
    user_id uuid NOT NULL,
    leader_zone_id uuid NOT NULL REFERENCES playback_zones(id) ON DELETE CASCADE,
    ordinal smallint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (device_id, user_id) REFERENCES playback_devices(id, user_id) ON DELETE CASCADE
);
CREATE INDEX playback_group_members_leader_idx ON playback_group_members(leader_zone_id);

ALTER TABLE playback_devices ADD COLUMN sync_delay_ms smallint NOT NULL DEFAULT 0
    CHECK (sync_delay_ms BETWEEN -500 AND 500);
