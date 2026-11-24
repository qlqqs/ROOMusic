ALTER TABLE playback_devices
    DROP CONSTRAINT playback_devices_kind_check,
    ADD CONSTRAINT playback_devices_kind_check CHECK (kind IN ('browser', 'native', 'upnp')),
    ADD COLUMN external_id TEXT,
    ADD COLUMN max_sample_rate INTEGER,
    ADD COLUMN volume_display_only BOOLEAN NOT NULL DEFAULT FALSE,
    ADD CONSTRAINT playback_devices_upnp_settings_check CHECK (
        (kind = 'upnp' AND external_id IS NOT NULL AND external_id <> '' AND
         max_sample_rate IS NOT NULL AND max_sample_rate IN (44100, 48000, 96000, 192000))
        OR (kind <> 'upnp' AND external_id IS NULL AND max_sample_rate IS NULL)
    ),
    ADD CONSTRAINT playback_devices_user_external_key UNIQUE (user_id, external_id);
