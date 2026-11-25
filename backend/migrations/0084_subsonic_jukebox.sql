CREATE TABLE subsonic_jukebox_settings (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    jukebox_zone_id uuid REFERENCES playback_zones(id) ON DELETE SET NULL
);

ALTER TABLE playback_zones ADD COLUMN last_played_at timestamptz;
