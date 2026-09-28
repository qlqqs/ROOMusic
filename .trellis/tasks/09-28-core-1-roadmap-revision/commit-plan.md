# 提交方案

## 工作提交

`docs: 按 D1–D6 修订 Core 1 路线、ADR 与架构图`

包含本任务全部已验证改动：

- `docs/architecture/core-1-roadmap.md`、`architecture-decisions.md`、`adr/0001`–`0004`：阶段顺序、Core 1 范围、ADR 决策记录与扩展边界两步走。
- `docs/architecture/core-1-architecture.html`（新增）：Core 1 目标架构图，含“06 扩展边界”。
- `frontend/DESIGN.md`、`.trellis/spec/guides/product-goals.md`、`.trellis/spec/frontend/hook-guidelines.md`：与裁决同步。
- `.trellis/tasks/09-27-classical-core-1/`：阶段映射、决策记录、门禁与备注。
- `.trellis/tasks/09-28-core-1-roadmap-revision/`：PRD、设计、执行计划、评估记录、上下文清单与本方案。
- 六个子任务 `.trellis/tasks/09-28-classical-*/`：PRD 初稿与上下文清单种子（均为 planning）。
- 旧任务 `09-04-music-client-experience` 从活动目录移入 `.trellis/tasks/archive/2026-09/`（已取代，meta 与说明已写入）。

无代码、依赖、锁文件、CI 或 Compose 改动；不推送、不部署。

## 分支

用户选择直接提交到 `main`（2026-09-28），不建分支。

## 提交后的收尾

1. 用 `task.py archive 09-28-core-1-roadmap-revision` 归档本任务（会自动生成归档提交）。
2. 把六个子任务与父任务中指向 `../09-28-core-1-roadmap-revision/` 的链接改为归档路径，随归档一并提交。
3. 记录会话日志。

## 状态

2026-09-28 用户回复“直接提交到main”：不建分支，工作提交直接进入 `main`，不推送。
