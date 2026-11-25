DROP INDEX playback_devices_subsonic_name_idx;
DELETE FROM playback_devices WHERE kind = 'subsonic';
ALTER TABLE playback_devices
    DROP CONSTRAINT playback_devices_kind_check,
    ADD CONSTRAINT playback_devices_kind_check CHECK (kind IN ('browser','native','upnp','cast','airplay','slimproto'));
