-- 默认版本与专辑级操作记录（阶段 M4 第一项）。
--
-- release_groups.default_release_id：管理员为这张专辑指定的默认版本，NULL 表示自动选择（年份最早）。不约束它属于这个组：
-- 归组会移动版本，而扫描和归组都不改这一列；它只在“这个发行可见、并且现在属于这个组”时生效（读取时的排序保证这一点），
-- 否则静默失效、回到自动规则，版本回来时重新生效。发行从不删除，ON DELETE SET NULL 只是保险。
-- release_groups.edit_revision：组的人工修改版本号，只有默认版本的设定、清除与撤销递增；归组递增的是发行的版本号，
-- 扫描从不碰组行，所以重扫之后默认版本保持是结构保证。
ALTER TABLE release_groups
    ADD COLUMN default_release_id UUID CONSTRAINT release_groups_default_release_fk REFERENCES releases(id) ON DELETE SET NULL,
    ADD COLUMN edit_revision BIGINT NOT NULL DEFAULT 1 CONSTRAINT release_groups_edit_revision_ck CHECK (edit_revision > 0);

-- operations 增加一种操作类型（设定默认版本）和一种目标类型（专辑，即 release_groups 的一行）。
-- 约束的名字与 0017 相同，只是放宽了允许的取值。
ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck
    CHECK (operation_type IN ('release_correction', 'release_group_change', 'release_group_default', 'revert'));
ALTER TABLE operations DROP CONSTRAINT operations_target_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_target_type_ck CHECK (target_type IN ('release', 'release_group'));
