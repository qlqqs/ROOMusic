DROP TABLE airplay_pairings;
DELETE FROM upnp_device_grants WHERE udn LIKE 'airplay:%';
DELETE FROM upnp_manual_devices WHERE kind = 'airplay';
ALTER TABLE upnp_manual_devices
    DROP CONSTRAINT upnp_manual_devices_kind_check,
    ADD CONSTRAINT upnp_manual_devices_kind_check CHECK (kind IN ('upnp', 'cast'));
ALTER TABLE upnp_settings DROP COLUMN airplay_enabled;
