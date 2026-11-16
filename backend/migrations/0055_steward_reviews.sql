ALTER TABLE steward_user_settings ADD COLUMN review_model text NOT NULL DEFAULT '';
ALTER TABLE steward_plan_items DROP CONSTRAINT steward_plan_items_decision_check;
ALTER TABLE steward_plan_items ADD CONSTRAINT steward_plan_items_decision_check CHECK (decision IN ('pending','accepted','declined','rejected_by_review'));
CREATE TABLE steward_reviews (
    id uuid PRIMARY KEY,
    plan_id uuid NOT NULL REFERENCES steward_plans(id) ON DELETE CASCADE,
    plan_digest text NOT NULL,
    evidence_digest text NOT NULL,
    policy_version integer NOT NULL CHECK (policy_version > 0),
    model text NOT NULL,
    status text NOT NULL CHECK (status IN ('completed','unavailable')),
    items jsonb NOT NULL CHECK (jsonb_typeof(items) = 'array'),
    usage jsonb NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX steward_reviews_plan_idx ON steward_reviews(plan_id,created_at DESC,id DESC);
