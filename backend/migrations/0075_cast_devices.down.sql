DELETE FROM playback_devices WHERE kind='cast';
DELETE FROM upnp_device_grants WHERE udn LIKE 'cast:%';
DELETE FROM upnp_manual_devices WHERE kind='cast';
DROP TABLE cast_device_formats;
ALTER TABLE upnp_manual_devices DROP COLUMN kind;
ALTER TABLE upnp_settings DROP COLUMN cast_enabled, DROP COLUMN upnp_enabled;
ALTER TABLE playback_devices
    DROP CONSTRAINT playback_devices_kind_check,
    DROP CONSTRAINT playback_devices_lan_settings_check,
    ADD CONSTRAINT playback_devices_kind_check CHECK (kind IN ('browser', 'native', 'upnp')),
    ADD CONSTRAINT playback_devices_upnp_settings_check CHECK (
        (kind = 'upnp' AND external_id IS NOT NULL AND external_id <> '' AND
         max_sample_rate IS NOT NULL AND max_sample_rate IN (44100, 48000, 96000, 192000))
        OR (kind <> 'upnp' AND external_id IS NULL AND max_sample_rate IS NULL)
    );
