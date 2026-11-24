ALTER TABLE upnp_settings
    ADD COLUMN upnp_enabled boolean NOT NULL DEFAULT true,
    ADD COLUMN cast_enabled boolean NOT NULL DEFAULT true;

ALTER TABLE upnp_manual_devices ADD COLUMN kind text NOT NULL DEFAULT 'upnp'
    CHECK (kind IN ('upnp', 'cast'));

CREATE TABLE cast_device_formats (
    udn text PRIMARY KEY,
    codec text NOT NULL CHECK (codec IN ('mp3','aac','flac')),
    max_sample_rate integer NOT NULL CHECK (
        max_sample_rate IN (44100,48000) OR (codec='flac' AND max_sample_rate=96000))
);

ALTER TABLE playback_devices
    DROP CONSTRAINT playback_devices_kind_check,
    DROP CONSTRAINT playback_devices_upnp_settings_check,
    ADD CONSTRAINT playback_devices_kind_check CHECK (kind IN ('browser','native','upnp','cast')),
    ADD CONSTRAINT playback_devices_lan_settings_check CHECK (
       (kind IN ('upnp','cast') AND external_id IS NOT NULL AND external_id <> '' AND
        max_sample_rate IS NOT NULL AND max_sample_rate IN (44100,48000,96000,192000))
       OR (kind NOT IN ('upnp','cast') AND external_id IS NULL AND max_sample_rate IS NULL)
    );
