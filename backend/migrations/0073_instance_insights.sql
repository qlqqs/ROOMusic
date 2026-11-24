CREATE TABLE instance_insights (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    instance_id uuid NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    notice_acknowledged_at timestamptz,
    last_attempt_at timestamptz,
    last_success_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now()
);

INSERT INTO instance_insights (instance_id) VALUES (gen_random_uuid());
