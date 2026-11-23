-- 会话
ALTER TABLE steward_conversations ADD COLUMN kind text NOT NULL DEFAULT 'chat'
  CHECK (kind IN ('chat','curation')),
  ADD CONSTRAINT steward_conversations_curation_mode_ck CHECK (kind <> 'curation' OR mode = 'steward');
CREATE INDEX steward_conversations_curation_idx ON steward_conversations (user_id, updated_at DESC, id) WHERE kind = 'curation';
ALTER TABLE steward_plans ADD COLUMN background boolean NOT NULL DEFAULT false;
ALTER TABLE steward_plan_items DROP CONSTRAINT steward_plan_items_decision_check,
  ADD CONSTRAINT steward_plan_items_decision_check CHECK (decision IN ('pending','accepted','declined','rejected_by_review','escalated'));
-- 待定项
ALTER TABLE curation_pending_items
  ADD COLUMN conversation_id uuid REFERENCES steward_conversations(id) ON DELETE SET NULL,
  ADD COLUMN claimed_at timestamptz,
  ADD COLUMN attempts smallint NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 3),
  ADD COLUMN review_retries smallint NOT NULL DEFAULT 0 CHECK (review_retries BETWEEN 0 AND 3),
  ADD COLUMN next_attempt_at timestamptz,
  ADD COLUMN shelved_reason text CHECK (octet_length(shelved_reason) <= 1000),
  ADD CONSTRAINT curation_pending_items_claim_ck CHECK (status <> 'in_progress' OR claimed_at IS NOT NULL);
CREATE INDEX curation_pending_items_due_idx ON curation_pending_items (status, next_attempt_at, created_at);
-- 例外
CREATE TABLE curation_model_exceptions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  pending_item_id uuid NOT NULL REFERENCES curation_pending_items(id) ON DELETE CASCADE,
  plan_item_id uuid NOT NULL UNIQUE,            -- 不设外键：删会话会级联删方案
  tool text NOT NULL CHECK (tool IN ('resolve_release_match','correct_release')),
  input jsonb NOT NULL, preview jsonb NOT NULL,
  provider text NOT NULL CHECK (provider ~ '^[a-z0-9._-]{1,64}$'),
  review_id uuid NOT NULL, review_model text NOT NULL,
  review_decision text NOT NULL CHECK (review_decision IN ('rejected','needs_human_confirmation')),
  review_source text NOT NULL CHECK (review_source IN ('model','policy')),
  risk text CHECK (risk IN ('low','medium','high')), reasons jsonb NOT NULL DEFAULT '[]',
  conversation_id uuid REFERENCES steward_conversations(id) ON DELETE SET NULL,
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','accepting','accepted','declined')),
  decided_by uuid REFERENCES users(id), decided_at timestamptz, operation_ids uuid[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((status = 'open') = (decided_by IS NULL)),
  CHECK ((status IN ('accepted','declined')) = (decided_at IS NOT NULL)));
CREATE INDEX curation_model_exceptions_open_idx ON curation_model_exceptions (status, created_at DESC, id);
-- 用量与设置
CREATE TABLE curation_model_usage (day date PRIMARY KEY,
  input_tokens bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
  output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
  updated_at timestamptz NOT NULL DEFAULT now());
ALTER TABLE curation_settings
  ADD COLUMN model text NOT NULL DEFAULT '' CHECK (octet_length(model) <= 200),
  ADD COLUMN model_daily_token_limit bigint DEFAULT 10000000 CHECK (model_daily_token_limit IS NULL OR model_daily_token_limit > 0),
  ADD COLUMN model_updated_at timestamptz;
