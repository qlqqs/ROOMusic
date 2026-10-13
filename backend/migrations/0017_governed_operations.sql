-- 受治理操作与修正覆盖层（阶段 M3）。
--
-- operations：每次成功提交的受控操作（发行字段修正、人工归组、撤销）一行。业务变化、这一行与幂等结果在同一事务里提交，
-- 所以不存在“有记录没变化”或“有变化没记录”。被拒绝或失败的尝试不写记录。before_state/after_state 是执行器定义的类型化
-- 前后状态（撤销的依据），response 是原样返回给客户端、也是同键重放时返回的响应。target_id 不设外键：记录可能比目标活得久，
-- target_type 以后会扩展。幂等键的作用域是同一个操作者（跨操作类型），指纹把操作类型、目标、预期版本与完整语义绑在一起。
CREATE TABLE operations (
    id UUID PRIMARY KEY,
    actor_id UUID NOT NULL REFERENCES users(id),
    operation_type TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id UUID NOT NULL,
    idempotency_key UUID NOT NULL,
    fingerprint TEXT NOT NULL,
    expected_revision BIGINT NOT NULL,
    result_revision BIGINT NOT NULL,
    before_state JSONB NOT NULL,
    after_state JSONB NOT NULL,
    response JSONB NOT NULL,
    reverts_operation_id UUID,
    request_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT operations_type_ck CHECK (operation_type IN ('release_correction', 'release_group_change', 'revert')),
    CONSTRAINT operations_target_type_ck CHECK (target_type IN ('release')),
    CONSTRAINT operations_fingerprint_ck CHECK (OCTET_LENGTH(fingerprint) BETWEEN 1 AND 128),
    CONSTRAINT operations_revision_ck CHECK (expected_revision > 0 AND result_revision = expected_revision + 1),
    -- 只有撤销记录指向被撤销的操作；一条操作最多被撤销一次。
    CONSTRAINT operations_reverts_ck CHECK ((operation_type = 'revert') = (reverts_operation_id IS NOT NULL)),
    CONSTRAINT operations_actor_key_uq UNIQUE (actor_id, idempotency_key),
    CONSTRAINT operations_reverts_uq UNIQUE (reverts_operation_id),
    CONSTRAINT operations_reverts_fk FOREIGN KEY (reverts_operation_id) REFERENCES operations(id)
);

CREATE INDEX operations_created_index ON operations (created_at DESC, id DESC);
CREATE INDEX operations_target_index ON operations (target_type, target_id, created_at DESC, id DESC);

-- 发行的人工修改版本号：只有受控操作（修正、归组、撤销）递增，扫描从不改它。字段修正与归组共用它，
-- 所以“撤销只能针对这个版本最近一次修改”是一句话的规则。
ALTER TABLE releases ADD COLUMN edit_revision BIGINT NOT NULL DEFAULT 1 CONSTRAINT releases_edit_revision_ck CHECK (edit_revision > 0);

-- 当前的修正意图：每个字段至多一行，没有行就是“没有修正”。mode = 'set' 表示改成某个值（等于扫描值也是有效修正，
-- 用来确认扫描值），mode = 'null' 表示明确设为空。标题与专辑艺人不能设空。扫描只写 releases，从不碰这张表，
-- 所以重扫不会覆盖修正是结构保证。operation_id 指向设下这个意图的操作；记录在执行器之后插入，所以外键提交时才检查。
CREATE TABLE release_corrections (
    release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    field TEXT NOT NULL,
    mode TEXT NOT NULL,
    text_value TEXT,
    year_value INTEGER,
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    PRIMARY KEY (release_id, field),
    CONSTRAINT release_corrections_field_ck CHECK (field IN ('title', 'album_artist', 'year', 'genre', 'label', 'catalog_number', 'edition', 'barcode')),
    CONSTRAINT release_corrections_mode_ck CHECK (mode IN ('set', 'null')),
    CONSTRAINT release_corrections_required_field_ck CHECK (NOT (field IN ('title', 'album_artist') AND mode = 'null')),
    CONSTRAINT release_corrections_value_ck CHECK (
        (mode = 'null' AND text_value IS NULL AND year_value IS NULL)
        OR (mode = 'set' AND field = 'year' AND text_value IS NULL AND year_value BETWEEN 1 AND 9999)
        OR (mode = 'set' AND field <> 'year' AND year_value IS NULL AND text_value IS NOT NULL
            AND BTRIM(text_value) <> '' AND OCTET_LENGTH(text_value) <= 4096)
    )
);

-- 发行的有效值：可修正字段在有 set 修正时取修正值，有 null 修正时为空，否则取扫描值；展示艺人 artist 跟随专辑艺人的修正
-- （扫描的 artist 本来就是“有专辑艺人取专辑艺人”）。其余列原样。列名与 releases 相同，catalog 的读取把 FROM releases
-- 换成 FROM release_effective releases 即可；扫描与身份规划仍读写 releases 本表。
-- 每个字段一次主键上的 LEFT JOIN：不被引用的列对应的连接会被优化器去掉，被引用的是一次主键查找，不需要聚合整张修正表。
CREATE VIEW release_effective AS
SELECT
    r.id,
    r.group_id,
    COALESCE(c_title.text_value, r.title) AS title,
    COALESCE(c_album_artist.text_value, r.artist) AS artist,
    r.created_at,
    r.source_root_id,
    r.relative_directory,
    r.candidate_anchor,
    r.candidate_kind,
    COALESCE(c_album_artist.text_value, r.album_artist) AS album_artist,
    CASE WHEN c_year.release_id IS NULL THEN r.year ELSE c_year.year_value END AS year,
    r.source_type,
    r.media_type,
    CASE WHEN c_genre.release_id IS NULL THEN r.genre ELSE c_genre.text_value END AS genre,
    CASE WHEN c_catalog_number.release_id IS NULL THEN r.catalog_number ELSE c_catalog_number.text_value END AS catalog_number,
    CASE WHEN c_edition.release_id IS NULL THEN r.edition ELSE c_edition.text_value END AS edition,
    CASE WHEN c_label.release_id IS NULL THEN r.label ELSE c_label.text_value END AS label,
    CASE WHEN c_barcode.release_id IS NULL THEN r.barcode ELSE c_barcode.text_value END AS barcode,
    r.edit_revision
FROM releases r
LEFT JOIN release_corrections c_title ON c_title.release_id = r.id AND c_title.field = 'title'
LEFT JOIN release_corrections c_album_artist ON c_album_artist.release_id = r.id AND c_album_artist.field = 'album_artist'
LEFT JOIN release_corrections c_year ON c_year.release_id = r.id AND c_year.field = 'year'
LEFT JOIN release_corrections c_genre ON c_genre.release_id = r.id AND c_genre.field = 'genre'
LEFT JOIN release_corrections c_catalog_number ON c_catalog_number.release_id = r.id AND c_catalog_number.field = 'catalog_number'
LEFT JOIN release_corrections c_edition ON c_edition.release_id = r.id AND c_edition.field = 'edition'
LEFT JOIN release_corrections c_label ON c_label.release_id = r.id AND c_label.field = 'label'
LEFT JOIN release_corrections c_barcode ON c_barcode.release_id = r.id AND c_barcode.field = 'barcode';
