DELETE FROM playback_devices WHERE kind='airplay';
DROP TABLE airplay_device_formats;
ALTER TABLE playback_devices
    DROP CONSTRAINT playback_devices_kind_check,
    DROP CONSTRAINT playback_devices_lan_settings_check,
    ADD CONSTRAINT playback_devices_kind_check CHECK (kind IN ('browser','native','upnp','cast')),
    ADD CONSTRAINT playback_devices_lan_settings_check CHECK (
       (kind IN ('upnp','cast') AND external_id IS NOT NULL AND external_id <> '' AND
        max_sample_rate IS NOT NULL AND max_sample_rate IN (44100,48000,96000,192000))
       OR (kind NOT IN ('upnp','cast') AND external_id IS NULL AND max_sample_rate IS NULL)
    );
