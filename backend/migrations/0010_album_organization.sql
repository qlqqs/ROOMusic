-- V0 自动整理的可重建字段与证据；旧列保持兼容。
ALTER TABLE releases ADD COLUMN IF NOT EXISTS candidate_anchor TEXT;
ALTER TABLE releases ADD COLUMN IF NOT EXISTS candidate_kind TEXT;
ALTER TABLE releases ADD COLUMN IF NOT EXISTS album_artist TEXT;
ALTER TABLE releases ADD COLUMN IF NOT EXISTS year INTEGER;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS source_kind TEXT NOT NULL DEFAULT 'physical';
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS source_identity TEXT;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS duration_seconds DOUBLE PRECISION;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS codec TEXT;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS sample_rate INTEGER;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS channels INTEGER;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS bitrate INTEGER;
CREATE UNIQUE INDEX IF NOT EXISTS releases_candidate_anchor_uq ON releases(source_root_id,candidate_anchor) WHERE candidate_anchor IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS tracks_source_identity_uq ON tracks(source_identity) WHERE source_identity IS NOT NULL;
-- 同一目录允许在 organizer 产生多个候选；旧直属目录唯一约束不再适用。
DROP INDEX IF EXISTS releases_source_directory;
CREATE TABLE IF NOT EXISTS release_field_decisions (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
 field_key TEXT NOT NULL, selected_value JSONB, selected_source TEXT NOT NULL, confidence TEXT NOT NULL,
 action TEXT NOT NULL, rule_id TEXT NOT NULL, candidates JSONB, reason TEXT, scan_run_id UUID REFERENCES scan_runs(id), observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(release_id,field_key)
);
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'release_field_decisions_confidence_ck'
          AND conrelid = 'release_field_decisions'::regclass
    ) THEN
        ALTER TABLE release_field_decisions
            ADD CONSTRAINT release_field_decisions_confidence_ck
            CHECK (confidence IN ('high','medium','low'));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'release_field_decisions_action_ck'
          AND conrelid = 'release_field_decisions'::regclass
    ) THEN
        ALTER TABLE release_field_decisions
            ADD CONSTRAINT release_field_decisions_action_ck
            CHECK (action IN ('auto_apply','uncertain_apply'));
    END IF;
END
$$;
CREATE TABLE IF NOT EXISTS release_grouping_evidence (
 id UUID PRIMARY KEY DEFAULT gen_random_uuid(), release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
 candidate_kind TEXT NOT NULL, rule_id TEXT NOT NULL, source_refs JSONB NOT NULL DEFAULT '[]', reason TEXT,
 scan_run_id UUID REFERENCES scan_runs(id), observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
