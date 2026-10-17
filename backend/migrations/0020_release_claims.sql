-- 认领（阶段 M4 第三项）：把已不可见的旧发行上的人工修改迁给扫描新建的发行。
--
-- 不新增表：旧发行认领之后一无所有，前后状态完整记在操作记录里，撤销以它为依据；“待认领”由 catalog 的谓词实时算出，
-- 扫描从不读写认领涉及的修正、归组、默认版本与核对。operations 增加一种操作类型（认领），目标仍是发行（认领后的新版本）。
-- 约束的名字与 0017 相同，只是放宽了允许的取值。
ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck
    CHECK (operation_type IN ('release_correction', 'release_group_change', 'release_group_default',
                              'release_issue_review', 'release_claim', 'revert'));

-- 按“来源发行”找认领记录与它们的撤销记录：认领与撤销的前后状态都带 source.release_id，其他类型的状态没有这个键，不进索引。
-- 操作记录页与发行的“修改记录”据此同时列出以某个发行为来源的认领。
CREATE INDEX operations_claim_source_index
    ON operations ((after_state #>> '{source,release_id}'), created_at DESC, id DESC)
    WHERE (after_state #>> '{source,release_id}') IS NOT NULL;
