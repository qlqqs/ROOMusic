ALTER TABLE upnp_settings ADD COLUMN slimproto_enabled boolean NOT NULL DEFAULT true;

CREATE TABLE slimproto_device_formats (
    udn text PRIMARY KEY CHECK (udn LIKE 'slim:%'),
    declared_max_sample_rate integer CHECK (declared_max_sample_rate > 0),
    max_sample_rate integer CHECK (max_sample_rate IN
        (44100,48000,88200,96000,176400,192000,352800,384000,705600,768000)),
    volume text NOT NULL DEFAULT 'number' CHECK (volume IN ('number','fixed'))
);

ALTER TABLE playback_devices
    DROP CONSTRAINT playback_devices_kind_check,
    DROP CONSTRAINT playback_devices_lan_settings_check,
    ADD CONSTRAINT playback_devices_kind_check CHECK (kind IN ('browser','native','upnp','cast','airplay','slimproto')),
    ADD CONSTRAINT playback_devices_lan_settings_check CHECK (
        (kind IN ('upnp','cast','airplay') AND external_id IS NOT NULL AND external_id <> '' AND
         max_sample_rate IS NOT NULL AND max_sample_rate IN (44100,48000,96000,192000))
        OR (kind = 'slimproto' AND external_id IS NOT NULL AND external_id <> '' AND
            max_sample_rate IS NOT NULL AND max_sample_rate BETWEEN 1 AND 768000)
        OR (kind NOT IN ('upnp','cast','airplay','slimproto') AND external_id IS NULL AND max_sample_rate IS NULL)
    );
