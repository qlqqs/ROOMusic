-- 扫描观察暂存与来源身份扩展。该迁移只增加约束/列，不修改音乐源文件。
CREATE TABLE IF NOT EXISTS scan_observations (
    id BIGSERIAL PRIMARY KEY,
    scan_run_id UUID NOT NULL REFERENCES scan_runs(id) ON DELETE CASCADE,
    root_id UUID NOT NULL REFERENCES library_roots(id) ON DELETE CASCADE,
    organization_scope TEXT NOT NULL CHECK (organization_scope <> ''),
    relative_path TEXT NOT NULL CHECK (relative_path <> ''),
    observation JSONB NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(scan_run_id, root_id, relative_path)
);

CREATE INDEX IF NOT EXISTS scan_observations_run_index
    ON scan_observations(scan_run_id, root_id, organization_scope, relative_path);

-- candidate split 允许同一目录拥有多个 stable anchors。
DROP INDEX IF EXISTS releases_source_directory;

ALTER TABLE tracks ADD COLUMN IF NOT EXISTS cue_sheet_path TEXT;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS cue_parent_relative_path TEXT;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS cue_referenced_file TEXT;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS cue_index_frames INTEGER;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS cue_end_frames INTEGER;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS cue_isrc TEXT;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS bit_depth INTEGER;

ALTER TABLE releases ADD COLUMN IF NOT EXISTS source_type TEXT;
ALTER TABLE releases ADD COLUMN IF NOT EXISTS media_type TEXT;
ALTER TABLE releases ADD COLUMN IF NOT EXISTS genre TEXT;
ALTER TABLE releases ADD COLUMN IF NOT EXISTS catalog_number TEXT;

-- 内容寻址文件可被多个 Release 复用；storage_key 不是关系身份。
ALTER TABLE release_artworks DROP CONSTRAINT IF EXISTS release_artworks_storage_key_key;
CREATE INDEX IF NOT EXISTS release_artworks_storage_key_index ON release_artworks(storage_key);

CREATE TABLE IF NOT EXISTS release_credits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    name TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 1 CHECK (position > 0),
    UNIQUE(release_id, role, name)
);

-- 升级 0010 的 v1/title-based anchor。需要 partition 的候选使用现有最小安全
-- relative_path；普通/多碟/Box 候选只使用稳定目录结构。
-- 多个旧 Release 若会收敛到同一 v2 anchor，或该 anchor 已存在，则保留原 v1 标记，
-- 交给后续成功重扫按 Track 来源身份迁移，避免升级时猜测 Release 合并语义。
WITH legacy AS (
    SELECT releases.id,
           releases.source_root_id,
           releases.source_root_id::text || ':v2:' ||
           COALESCE(NULLIF(releases.candidate_kind, ''), 'ordinary_directory') || ':' ||
           COALESCE(releases.relative_directory, '') || ':' ||
           CASE
               WHEN COALESCE(releases.candidate_kind, 'ordinary_directory') IN ('loose_album','loose_unknown','same_dir_split')
               THEN COALESCE(
                   'source:' || (
                       SELECT MIN(tracks.relative_path)
                       FROM media JOIN tracks ON tracks.medium_id=media.id
                       WHERE media.release_id=releases.id
                   ),
                   'legacy-' || releases.id::text
               )
               ELSE ''
           END AS anchor
    FROM releases
    WHERE releases.source_root_id IS NOT NULL
      AND (releases.candidate_anchor IS NULL OR releases.candidate_anchor LIKE releases.source_root_id::text || ':v1:%')
), safe_legacy AS (
    SELECT candidate.id, candidate.anchor
    FROM legacy candidate
    WHERE NOT EXISTS (
        SELECT 1
        FROM legacy other
        WHERE other.source_root_id = candidate.source_root_id
          AND other.anchor = candidate.anchor
          AND other.id <> candidate.id
    )
      AND NOT EXISTS (
        SELECT 1
        FROM releases existing
        WHERE existing.source_root_id = candidate.source_root_id
          AND existing.candidate_anchor = candidate.anchor
          AND existing.id <> candidate.id
    )
)
UPDATE releases SET candidate_anchor = safe_legacy.anchor
FROM safe_legacy WHERE releases.id = safe_legacy.id;

UPDATE tracks
SET source_identity = source_root_id::text || ':physical:v1:' || relative_path
WHERE source_root_id IS NOT NULL
  AND source_kind <> 'cue_virtual'
  AND (source_identity IS NULL OR source_identity = source_root_id::text || ':' || relative_path);

WITH ranked AS (
    SELECT id,
           first_value(id) OVER (PARTITION BY release_id, position ORDER BY id) AS canonical_id,
           row_number() OVER (PARTITION BY release_id, position ORDER BY id) AS ordinal
    FROM media
)
UPDATE tracks SET medium_id = ranked.canonical_id
FROM ranked WHERE ranked.ordinal > 1 AND tracks.medium_id = ranked.id;

WITH ranked AS (
    SELECT id, row_number() OVER (PARTITION BY release_id, position ORDER BY id) AS ordinal
    FROM media
)
DELETE FROM media USING ranked WHERE ranked.ordinal > 1 AND media.id = ranked.id;

CREATE UNIQUE INDEX IF NOT EXISTS media_release_position_uq
    ON media(release_id, position);
DELETE FROM track_observations observation
USING (
    SELECT id FROM (
        SELECT id, row_number() OVER (PARTITION BY track_id, field_name ORDER BY observed_at DESC, id DESC) AS ordinal
        FROM track_observations
        WHERE field_name IS NOT NULL
    ) ranked
    WHERE ranked.ordinal > 1
) duplicate
WHERE observation.id = duplicate.id;
CREATE UNIQUE INDEX IF NOT EXISTS track_observations_current_uq
    ON track_observations(track_id, field_name) WHERE field_name IS NOT NULL;
-- 0010 曾允许 append-only evidence；先收敛到每个 Release 最新一条，再建立 current 约束。
DELETE FROM release_grouping_evidence evidence
USING (
    SELECT id FROM (
        SELECT id, row_number() OVER (PARTITION BY release_id ORDER BY observed_at DESC, id DESC) AS ordinal
        FROM release_grouping_evidence
    ) ranked
    WHERE ranked.ordinal > 1
) duplicate
WHERE evidence.id = duplicate.id;
CREATE UNIQUE INDEX IF NOT EXISTS release_grouping_evidence_current_uq
    ON release_grouping_evidence(release_id);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'tracks_cue_index_frames_ck'
          AND conrelid = 'tracks'::regclass
    ) THEN
        ALTER TABLE tracks ADD CONSTRAINT tracks_cue_index_frames_ck
            CHECK (cue_index_frames IS NULL OR cue_index_frames >= 0);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'tracks_cue_end_frames_ck'
          AND conrelid = 'tracks'::regclass
    ) THEN
        ALTER TABLE tracks ADD CONSTRAINT tracks_cue_end_frames_ck
            CHECK (cue_end_frames IS NULL OR cue_end_frames >= 0);
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'tracks_bit_depth_ck'
          AND conrelid = 'tracks'::regclass
    ) THEN
        ALTER TABLE tracks ADD CONSTRAINT tracks_bit_depth_ck
            CHECK (bit_depth IS NULL OR bit_depth > 0);
    END IF;
END
$$;
