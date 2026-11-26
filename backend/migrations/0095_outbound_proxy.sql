CREATE TABLE outbound_proxy_settings (
    id smallint PRIMARY KEY CHECK (id = 1),
    enabled boolean NOT NULL DEFAULT false,
    scheme text NOT NULL DEFAULT 'http' CHECK (scheme IN ('http', 'socks5')),
    host text NOT NULL DEFAULT '',
    port integer CHECK (port BETWEEN 1 AND 65535),
    username text NOT NULL DEFAULT '',
    password_ciphertext bytea,
    version bigint NOT NULL DEFAULT 1,
    updated_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    CHECK (NOT enabled OR (host <> '' AND port IS NOT NULL))
);

INSERT INTO outbound_proxy_settings (id) VALUES (1);
