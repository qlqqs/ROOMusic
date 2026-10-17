-- 元数据问题的核对（阶段 M4 第二项）。
--
-- release_issue_reviews：管理员把一个版本的某类问题标记为“已核对”时，记住当时的依据，即扫描结论里决定“情况是否变了”的那部分
-- （归组冲突是原因与来源路径，来源类型、介质类型是原因、选出的值与候选，缺失曲目是每首最后一次被看到的时间）。
-- 每个版本每类问题至多一行；核对人与时间来自 operation_id 指向的操作记录。扫描从不读写这张表，重扫之后核对行还在，
-- 是否仍然有效由读取时比较依据决定：依据变了就重新计入“需要关注”（缺失曲目逐首比较）。过期的行不删除，再次核对时覆盖。
-- 发行从不删除，ON DELETE CASCADE 只是保险；记录在执行器之后才插入，所以外键提交时才检查。
CREATE TABLE release_issue_reviews (
    release_id UUID NOT NULL REFERENCES releases(id) ON DELETE CASCADE,
    issue TEXT NOT NULL,
    basis JSONB NOT NULL,
    operation_id UUID NOT NULL REFERENCES operations(id) DEFERRABLE INITIALLY DEFERRED,
    PRIMARY KEY (release_id, issue),
    CONSTRAINT release_issue_reviews_issue_ck CHECK (issue IN ('grouping', 'missing_tracks', 'source_type', 'media_type')),
    CONSTRAINT release_issue_reviews_basis_ck CHECK (jsonb_typeof(basis) = 'object')
);

-- operations 增加一种操作类型（核对问题），目标仍是发行。约束的名字与 0017 相同，只是放宽了允许的取值。
ALTER TABLE operations DROP CONSTRAINT operations_type_ck;
ALTER TABLE operations ADD CONSTRAINT operations_type_ck
    CHECK (operation_type IN ('release_correction', 'release_group_change', 'release_group_default', 'release_issue_review', 'revert'));
