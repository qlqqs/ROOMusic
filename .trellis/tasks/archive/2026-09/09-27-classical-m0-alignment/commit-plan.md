# M0 提交计划（已执行）

## 批次

已批准一个工作提交：`docs: 对齐 Classical 设计与 Core 1 路线`。
内容为本轮设计/规范/ADR 文档，以及同一会话前一轮创建并在本轮更新的父子任务规划记录。
不包含业务代码、依赖、原型、旧任务、数据库或构建产物。

用户于 2026-09-27 回复“提交”，明确批准本计划。已从 `main` 创建
`docs/classical-m0-alignment` 分支并同步 M0 的任务分支记录，`base_branch` 保持 `main`；
按下列显式路径提交。
不推送、不 amend；M0 归档和会话日志属于工作提交后的独立 Trellis 收尾，不混入工作提交。
提交这些草案不等于批准其中的架构建议，也不启动 M1/B0。

## 文件清单

- `.trellis/spec/backend/core0-runtime-contracts.md`
- `.trellis/spec/backend/index.md`
- `.trellis/spec/frontend/component-guidelines.md`
- `.trellis/spec/frontend/directory-structure.md`
- `.trellis/spec/frontend/hook-guidelines.md`
- `.trellis/spec/frontend/index.md`
- `.trellis/spec/frontend/player-design-guidelines.md`
- `.trellis/spec/frontend/quality-guidelines.md`
- `.trellis/spec/frontend/state-management.md`
- `.trellis/spec/frontend/type-safety.md`
- `.trellis/spec/guides/engineering-principles.md`
- `.trellis/spec/guides/index.md`
- `.trellis/spec/guides/product-goals.md`
- `.trellis/tasks/09-27-classical-core-1/check.jsonl`
- `.trellis/tasks/09-27-classical-core-1/design.md`
- `.trellis/tasks/09-27-classical-core-1/implement.jsonl`
- `.trellis/tasks/09-27-classical-core-1/implement.md`
- `.trellis/tasks/09-27-classical-core-1/prd.md`
- `.trellis/tasks/09-27-classical-core-1/task.json`
- `.trellis/tasks/09-27-classical-m0-alignment/check.jsonl`
- `.trellis/tasks/09-27-classical-m0-alignment/commit-plan.md`
- `.trellis/tasks/09-27-classical-m0-alignment/design.md`
- `.trellis/tasks/09-27-classical-m0-alignment/implement.jsonl`
- `.trellis/tasks/09-27-classical-m0-alignment/implement.md`
- `.trellis/tasks/09-27-classical-m0-alignment/prd.md`
- `.trellis/tasks/09-27-classical-m0-alignment/research/baseline.md`
- `.trellis/tasks/09-27-classical-m0-alignment/task.json`
- `.trellis/tasks/09-27-classical-m0-alignment/verification.md`
- `README.md`
- `docs/architecture/adr/0001-frontend-foundations.md`
- `docs/architecture/adr/0002-metadata-overlays.md`
- `docs/architecture/adr/0003-governed-operations.md`
- `docs/architecture/adr/0004-media-delivery.md`
- `docs/architecture/architecture-decisions.md`
- `docs/architecture/core-1-roadmap.md`
- `frontend/DESIGN.md`

## 未识别变更

当前没有：已跟踪文档由主会话更新，新增设计/ADR 由指定代理写入，两任务目录来自同一
会话的任务创建与本轮审批。实际提交前再次核对；若出现新的用户/并行改动，不自动包含。

## 当前门禁

`verification.md` 中的独立文档检查已通过；提交前再次重跑最小门禁。
此前“批准”仅授权 M0 实施；本轮“提交”明确授权本计划的 Git 提交与 M0 收尾。
不推送，不归档父任务或旧任务，不将提交草案视作技术采纳或后续实施授权。

## 执行结果

工作提交：`850d274`（`docs: 对齐 Classical 设计与 Core 1 路线`）。以上文件清单记录
工作提交时的原路径；随后仅 M0 迁入 `archive/2026-09/`，父子任务的有效链接和上下文
引用随归档修复。不改写原提交清单或归档前验证命令，不归档旧任务，不推送。
