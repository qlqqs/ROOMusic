-- 每个艺人名或该艺人的专辑名保留一条结果。原文参与摘要，模型和提示词版本只作溯源。
CREATE TABLE curation_patrol_names (
    id text PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('artist','album')),
    artist_id uuid REFERENCES artists(id),
    name text NOT NULL,
    needs_patrol boolean NOT NULL DEFAULT true,
    pending_id uuid REFERENCES curation_pending_items(id) ON DELETE SET NULL,
    original_name text NOT NULL DEFAULT '',
    latin_name text NOT NULL DEFAULT '',
    original_key text NOT NULL DEFAULT '',
    latin_key text NOT NULL DEFAULT '',
    confidence text CHECK (confidence IN ('high','low')),
    issues jsonb NOT NULL DEFAULT '[]',
    model text,
    prompt_version smallint,
    patrolled_at timestamptz
);
CREATE INDEX curation_patrol_original ON curation_patrol_names USING hash(original_key)
    WHERE confidence='high' AND patrolled_at IS NOT NULL;
CREATE INDEX curation_patrol_latin ON curation_patrol_names USING hash(latin_key)
    WHERE confidence='high' AND patrolled_at IS NOT NULL;
CREATE INDEX curation_patrol_pending ON curation_patrol_names(pending_id);

CREATE INDEX curation_patrol_waiting ON curation_patrol_names(kind DESC,artist_id,id)
    WHERE needs_patrol AND pending_id IS NULL;
CREATE INDEX curation_patrol_artist_kind ON curation_patrol_names(artist_id,kind);
CREATE TABLE curation_patrol_inputs (
    release_id uuid NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    name_id text NOT NULL REFERENCES curation_patrol_names(id) DEFERRABLE INITIALLY DEFERRED,
    PRIMARY KEY(release_id,name_id)
);
CREATE INDEX curation_patrol_input_releases ON curation_patrol_inputs(name_id,release_id);
