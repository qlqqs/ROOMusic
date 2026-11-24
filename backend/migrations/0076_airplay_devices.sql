ALTER TABLE upnp_settings ADD COLUMN airplay_enabled boolean NOT NULL DEFAULT true;

ALTER TABLE upnp_manual_devices
    DROP CONSTRAINT upnp_manual_devices_kind_check,
    ADD CONSTRAINT upnp_manual_devices_kind_check CHECK (kind IN ('upnp', 'cast', 'airplay'));

-- 自动发现的接收端也可以配对，不依赖手动登记。
CREATE TABLE airplay_pairings (
    udn text PRIMARY KEY CHECK (udn LIKE 'airplay:%'),
    credentials_ciphertext bytea NOT NULL
);
