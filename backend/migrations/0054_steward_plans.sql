ALTER TABLE operations ADD COLUMN origin jsonb;
ALTER TABLE steward_conversations ADD COLUMN auto_approved_tools text[] NOT NULL DEFAULT '{}';
ALTER TABLE steward_tool_calls DROP CONSTRAINT steward_tool_calls_status_check;
ALTER TABLE steward_tool_calls ADD CONSTRAINT steward_tool_calls_status_check CHECK (status IN ('running','succeeded','failed','awaiting_confirmation'));
ALTER TABLE steward_tool_calls ADD COLUMN idempotency_key uuid NOT NULL DEFAULT gen_random_uuid();

CREATE TABLE steward_plans (
    id uuid PRIMARY KEY,
    conversation_id uuid NOT NULL REFERENCES steward_conversations(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','applied','declined','expired','superseded')),
    digest text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX steward_plans_pending_idx ON steward_plans(conversation_id) WHERE status='pending';
CREATE INDEX steward_plans_conversation_idx ON steward_plans(conversation_id);
CREATE TABLE steward_plan_items (
    id uuid PRIMARY KEY,
    plan_id uuid NOT NULL REFERENCES steward_plans(id) ON DELETE CASCADE,
    tool_call_id uuid NOT NULL REFERENCES steward_tool_calls(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position >= 0),
    tool_name text NOT NULL,
    input jsonb NOT NULL,
    preview jsonb NOT NULL,
    revertible boolean NOT NULL,
    idempotency_key uuid NOT NULL,
    decision text NOT NULL DEFAULT 'pending' CHECK (decision IN ('pending','accepted','declined')),
    result jsonb,
    operation_id uuid REFERENCES operations(id),
    UNIQUE(plan_id, position),
    UNIQUE(tool_call_id)
);
