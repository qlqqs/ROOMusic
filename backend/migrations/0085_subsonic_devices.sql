ALTER TABLE playback_devices
    DROP CONSTRAINT playback_devices_kind_check,
    ADD CONSTRAINT playback_devices_kind_check CHECK (kind IN ('browser','native','upnp','cast','airplay','slimproto','subsonic'));

CREATE UNIQUE INDEX playback_devices_subsonic_name_idx ON playback_devices(user_id, name) WHERE kind = 'subsonic';
