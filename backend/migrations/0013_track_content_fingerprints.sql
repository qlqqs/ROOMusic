-- 曲目内容指纹与扫描身份规划列：同一 root 内的改名与移动按音频内容保守重绑定曲目，不再随路径换 ID。
-- 该迁移只增加列、放宽约束，不改写已有数据，也不修改音乐源文件。
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS content_fingerprint TEXT;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS source_size BIGINT;
ALTER TABLE tracks ADD COLUMN IF NOT EXISTS source_modified_at TIMESTAMPTZ;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'tracks_content_fingerprint_ck'
          AND conrelid = 'tracks'::regclass
    ) THEN
        ALTER TABLE tracks ADD CONSTRAINT tracks_content_fingerprint_ck
            CHECK (content_fingerprint IS NULL OR (content_fingerprint <> '' AND OCTET_LENGTH(content_fingerprint) <= 256));
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'tracks_source_size_ck'
          AND conrelid = 'tracks'::regclass
    ) THEN
        ALTER TABLE tracks ADD CONSTRAINT tracks_source_size_ck
            CHECK (source_size IS NULL OR source_size >= 0);
    END IF;
END
$$;

-- 内容被替换的旧曲目会失去来源身份，并与同一路径上的新曲目并存；路径唯一性只约束仍有来源身份的行。
CREATE UNIQUE INDEX IF NOT EXISTS tracks_bound_source_path_uq
    ON tracks(source_root_id, relative_path) WHERE source_identity IS NOT NULL;
ALTER TABLE tracks DROP CONSTRAINT IF EXISTS tracks_source_root_id_relative_path_key;

-- 配对时按 root 与指纹查找本 root 中本轮消失的曲目。
CREATE INDEX IF NOT EXISTS tracks_root_fingerprint_index
    ON tracks(source_root_id, content_fingerprint) WHERE content_fingerprint IS NOT NULL;

-- 暂存行上的规划列：来源身份与指纹依据在暂存时写入，其余由扫描的规划阶段填写；
-- 暂存行随扫描清理，不设外键。
ALTER TABLE scan_observations
    ADD COLUMN IF NOT EXISTS source_identity TEXT,
    ADD COLUMN IF NOT EXISTS fingerprint_path TEXT,
    ADD COLUMN IF NOT EXISTS source_size BIGINT,
    ADD COLUMN IF NOT EXISTS source_modified_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS content_fingerprint TEXT,
    ADD COLUMN IF NOT EXISTS cue_index_frames INTEGER,
    ADD COLUMN IF NOT EXISTS cue_end_frames INTEGER,
    ADD COLUMN IF NOT EXISTS candidate_key TEXT,
    ADD COLUMN IF NOT EXISTS matched_track_id UUID,
    ADD COLUMN IF NOT EXISTS planned_track_id UUID,
    ADD COLUMN IF NOT EXISTS content_changed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS withheld BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS planned BOOLEAN NOT NULL DEFAULT FALSE;

-- 计算出指纹后按文件回写暂存行（同一个 CUE 父文件对应多行），以及后续按候选键写入 Release 分配。
CREATE INDEX IF NOT EXISTS scan_observations_fingerprint_path_index
    ON scan_observations(scan_run_id, root_id, fingerprint_path);
CREATE INDEX IF NOT EXISTS scan_observations_candidate_index
    ON scan_observations(scan_run_id, root_id, candidate_key);
