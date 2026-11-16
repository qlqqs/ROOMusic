CREATE TABLE steward_user_settings (
    user_id uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    endpoint text NOT NULL DEFAULT '',
    key_ciphertext bytea,
    key_last_four text NOT NULL DEFAULT '',
    protocol text NOT NULL DEFAULT '' CHECK (protocol IN ('', 'openai', 'messages')),
    model text NOT NULL DEFAULT '',
    display_name text NOT NULL DEFAULT '',
    probe_result jsonb,
    probed_at timestamptz,
    overrides jsonb NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (char_length(key_last_four) <= 4),
    CHECK (key_ciphertext IS NOT NULL OR key_last_four = ''),
    CHECK ((probe_result IS NULL) = (probed_at IS NULL))
);

CREATE TABLE steward_instance_settings (
    id integer PRIMARY KEY CHECK (id = 1),
    members_enabled boolean NOT NULL DEFAULT true
);
INSERT INTO steward_instance_settings (id) VALUES (1);

CREATE TABLE steward_egress_allowlist (
    host text NOT NULL CHECK (host <> ''),
    port integer NOT NULL CHECK (port BETWEEN 1 AND 65535),
    added_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (host, port)
);

CREATE TABLE steward_conversations (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title text NOT NULL DEFAULT '',
    mode text NOT NULL DEFAULT 'assistant' CHECK (mode IN ('assistant', 'steward', 'operator')),
    model text NOT NULL,
    status text NOT NULL DEFAULT 'idle' CHECK (status IN ('idle', 'running')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX steward_conversations_user_updated_idx
    ON steward_conversations (user_id, updated_at DESC, id);

CREATE TABLE steward_messages (
    id uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES steward_conversations(id) ON DELETE CASCADE,
    sequence bigint NOT NULL CHECK (sequence > 0),
    role text NOT NULL CHECK (role IN ('user', 'assistant', 'tool')),
    -- 使用 json 保留适配器提供的不透明块及原始 JSON 文本，不做 jsonb 规范化。
    content json NOT NULL,
    input_tokens bigint CHECK (input_tokens >= 0),
    output_tokens bigint CHECK (output_tokens >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (conversation_id, sequence)
);

CREATE TABLE steward_tool_calls (
    id uuid PRIMARY KEY,
    message_id uuid NOT NULL REFERENCES steward_messages(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    tool_name text NOT NULL,
    input json NOT NULL,
    output json,
    truncated boolean NOT NULL DEFAULT false,
    untrusted boolean NOT NULL DEFAULT false,
    status text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'succeeded', 'failed')),
    error_code text,
    duration_ms bigint CHECK (duration_ms >= 0),
    UNIQUE (message_id, position)
);
CREATE INDEX steward_tool_calls_message_idx ON steward_tool_calls (message_id);
