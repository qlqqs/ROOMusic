# 扫描候选持久化与证据闭环

## Goal

将 organizer 完整接入候选级持久化并覆盖多碟、CUE 与证据写入

## Requirements

- 将 `scanRoot` 从逐文件 Release 写入切换为按候选短事务持久化，保持 Track 来源身份与重扫幂等。
- 持久化多碟、Box leaf、散落文件、同目录拆分的 candidate anchor、field decisions、grouping evidence。
- CUE 虚拟轨与真实分轨共存时去重，失败/取消扫描不执行 missing 对账。

## Acceptance Criteria

- [ ] 相同 observations 任意遍历顺序产生相同候选、Medium/Track 结构和 evidence。
- [ ] 重扫不复制 present 实体，候选变化更新归属且旧空壳不出现在普通列表。
- [ ] PostgreSQL 集成测试覆盖事务回滚、幂等、CUE identity 与 complete-only missing。

## Notes

- Keep `prd.md` focused on requirements, constraints, and acceptance criteria.
- Lightweight tasks can remain PRD-only.
- For complex tasks, add `design.md` for technical design and `implement.md` for execution planning before `task.py start`.
