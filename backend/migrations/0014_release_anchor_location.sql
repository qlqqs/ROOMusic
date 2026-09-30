-- Release 按曲目成员关系匹配：候选锚点降为定位信息，只记录最近一次的位置，不再唯一。
-- 这样目录内容整体替换后，被放弃的旧 Release 与新 Release 可以拥有同一个锚点。
-- 该迁移只放宽约束、增加列，不改写已有数据。
DROP INDEX IF EXISTS releases_candidate_anchor_uq;

-- 暂存行上的 Release 分配：规划阶段为获胜的候选写入要沿用的 Release，其余候选写入时新建。
ALTER TABLE scan_observations ADD COLUMN IF NOT EXISTS planned_release_id UUID;
