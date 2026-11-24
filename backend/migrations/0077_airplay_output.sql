CREATE TABLE airplay_device_formats (
    udn text PRIMARY KEY CHECK (udn LIKE 'airplay:%'),
    max_sample_rate integer NOT NULL,
    max_bit_depth integer NOT NULL,
    CHECK ((max_sample_rate, max_bit_depth) IN ((44100, 16), (48000, 24)))
);

ALTER TABLE playback_devices
    DROP CONSTRAINT playback_devices_kind_check,
    DROP CONSTRAINT playback_devices_lan_settings_check,
    ADD CONSTRAINT playback_devices_kind_check CHECK (kind IN ('browser','native','upnp','cast','airplay')),
    ADD CONSTRAINT playback_devices_lan_settings_check CHECK (
        (kind IN ('upnp','cast','airplay') AND external_id IS NOT NULL AND external_id <> '' AND
         max_sample_rate IS NOT NULL AND max_sample_rate IN (44100,48000,96000,192000))
        OR (kind NOT IN ('upnp','cast','airplay') AND external_id IS NULL AND max_sample_rate IS NULL)
    );
