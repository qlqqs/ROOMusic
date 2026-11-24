CREATE TABLE upnp_settings (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    enabled BOOLEAN NOT NULL DEFAULT false,
    shared BOOLEAN NOT NULL DEFAULT true
);
INSERT INTO upnp_settings(id) VALUES (1);

CREATE TABLE upnp_manual_devices (
    udn TEXT PRIMARY KEY,
    address TEXT NOT NULL,
    name TEXT NOT NULL,
    model TEXT NOT NULL,
    capabilities JSONB NOT NULL
);

-- 自动发现的设备也能分配账号，因此 UDN 不依赖手动登记。
CREATE TABLE upnp_device_grants (
    udn TEXT NOT NULL,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (udn, user_id)
);
CREATE INDEX upnp_device_grants_user_idx ON upnp_device_grants(user_id, udn);
