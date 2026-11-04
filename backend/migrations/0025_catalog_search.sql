-- 搜索是可重建投影；迁移只建空表和登记目标，不扫描存量目录。
CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

CREATE TABLE catalog_projection_state (
    capability TEXT PRIMARY KEY CHECK (capability IN ('search')),
    target_generation BIGINT NOT NULL CHECK (target_generation > 0),
    textnorm_version BIGINT NOT NULL CHECK (textnorm_version > 0),
    search_projection_version BIGINT NOT NULL CHECK (search_projection_version > 0),
    published_generation BIGINT NOT NULL DEFAULT 0,
    published_textnorm_version BIGINT NOT NULL DEFAULT 0,
    published_search_projection_version BIGINT NOT NULL DEFAULT 0,
    build_id UUID NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('preparing', 'building', 'ready', 'failed')),
    catalog_epoch BIGINT NOT NULL CHECK (catalog_epoch > 0),
    manifest_sealed BOOLEAN NOT NULL DEFAULT FALSE,
    live_updated BIGINT NOT NULL DEFAULT 0 CHECK (live_updated >= 0),
    started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT catalog_projection_ready_ck CHECK (status <> 'ready' OR
        (manifest_sealed AND target_generation = published_generation AND
         textnorm_version = published_textnorm_version AND
         search_projection_version = published_search_projection_version))
);
INSERT INTO catalog_projection_state(capability,target_generation,textnorm_version,search_projection_version,build_id,status,catalog_epoch)
VALUES ('search',1,1,1,gen_random_uuid(),'preparing',1);

CREATE TABLE catalog_projection_subject_state (
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('release', 'track')),
    subject_id UUID NOT NULL,
    input_revision BIGINT NOT NULL DEFAULT 0 CHECK (input_revision >= 0),
    search_applied_generation BIGINT NOT NULL DEFAULT 0,
    search_applied_input_revision BIGINT NOT NULL DEFAULT -1,
    search_title TEXT NOT NULL DEFAULT '' CHECK (octet_length(search_title) <= 65536),
    PRIMARY KEY (subject_kind, subject_id)
);
CREATE INDEX catalog_projection_subject_gap_idx ON catalog_projection_subject_state
    (subject_kind, search_applied_generation, subject_id);
CREATE INDEX catalog_projection_subject_stale_idx ON catalog_projection_subject_state
    (subject_kind, subject_id) WHERE search_applied_input_revision <> input_revision;

-- 阶段分母一经登记就不变；发布检查发现的缺口使用单独 repair 阶段。
CREATE TABLE catalog_projection_phases (
    build_id UUID NOT NULL,
    phase TEXT NOT NULL,
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('release', 'track')),
    total BIGINT NOT NULL CHECK (total >= 0),
    completed BIGINT NOT NULL DEFAULT 0 CHECK (completed >= 0 AND completed <= total),
    PRIMARY KEY (build_id, phase)
);
CREATE TABLE catalog_projection_work (
    build_id UUID NOT NULL,
    phase TEXT NOT NULL,
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('release', 'track')),
    subject_id UUID NOT NULL,
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    completed_input_revision BIGINT,
    PRIMARY KEY (build_id, phase, subject_kind, subject_id),
    FOREIGN KEY (build_id, phase) REFERENCES catalog_projection_phases(build_id, phase)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE INDEX catalog_projection_work_pending_idx ON catalog_projection_work(build_id, phase, subject_id)
    WHERE NOT completed;

CREATE TABLE catalog_search_values (
    id UUID PRIMARY KEY,
    release_id UUID REFERENCES releases(id) ON DELETE CASCADE,
    track_id UUID REFERENCES tracks(id) ON DELETE CASCADE,
    field TEXT NOT NULL,
    role TEXT,
    position INTEGER NOT NULL CHECK (position > 0),
    original_value TEXT NOT NULL CHECK (octet_length(original_value) BETWEEN 1 AND 4096),
    normalized_value TEXT NOT NULL CHECK (octet_length(normalized_value) BETWEEN 1 AND 65536),
    normalization_version BIGINT NOT NULL CHECK (normalization_version > 0),
    projection_generation BIGINT NOT NULL CHECK (projection_generation > 0),
    short_grams TEXT[] NOT NULL,
    CONSTRAINT catalog_search_values_subject_ck CHECK (num_nonnulls(release_id,track_id) = 1),
    CONSTRAINT catalog_search_values_field_ck CHECK (
        (release_id IS NOT NULL AND field IN ('release_title','release_artist','album_artist','label','catalog_number')) OR
        (track_id IS NOT NULL AND field IN ('track_title','track_artist','credit'))),
    CONSTRAINT catalog_search_values_role_ck CHECK (
        (field = 'credit' AND role IS NOT NULL AND role IN ('composer','conductor','performer','producer')) OR
        (field <> 'credit' AND role IS NULL)),
    UNIQUE NULLS NOT DISTINCT (release_id,track_id,field,role,position)
);
CREATE INDEX catalog_search_values_trgm_idx ON catalog_search_values USING gin(normalized_value public.gin_trgm_ops);
CREATE INDEX catalog_search_values_short_idx ON catalog_search_values USING gin(short_grams);
CREATE INDEX catalog_search_values_release_idx ON catalog_search_values(release_id);
CREATE INDEX catalog_search_values_track_idx ON catalog_search_values(track_id);

CREATE INDEX catalog_projection_work_subject_idx ON catalog_projection_work(build_id,subject_kind,subject_id);

-- 完全相同层用定长哈希定位后再比较完整键，避免为排在首屏之外的较低等级扇出。
CREATE INDEX catalog_search_values_exact_idx ON catalog_search_values(md5(normalized_value));
-- 前缀仅用于增量排序，查询仍用完整标题/UUID打平；空标题也有subject行。
CREATE INDEX catalog_projection_title_order_idx ON catalog_projection_subject_state
    (subject_kind, (left(search_title,128) COLLATE "C"), subject_id);

-- 组的命中/代表/摘要都按当前成员查找；已有基线没有group_id索引。
CREATE INDEX catalog_search_release_group_idx ON releases(group_id,id);
