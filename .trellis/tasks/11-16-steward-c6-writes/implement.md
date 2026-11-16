# 管家 C6 执行计划

分支 `steward-c6-writes`，依赖 C5 已合入。后端实现 `gpt-6-astra` high，测试由另一个子代理；前端 Opus 5.5（high）。

- [x] 1 后端：迁移 0054（来源列、方案表、调用状态、会话列）；`operations.Spec.Origin`；`Preview`；治理工具与撤销工具。
- [x] 2 后端：调用入口的 `shared` 路径、方案生成与挂起、确认接口、模式、MCP 直接执行。
- [x] 3 后端测试与样本；规范同步（运行时合同、治理合同、`agent-and-operation-guidelines.md` 的 operator 条文与错误码）。
- [x] 4 前端：方案卡片、结果与撤销、模式按钮、操作记录来源；组件测试与一条端到端冒烟。
- [x] 5 独立审查（只读），修复；截图存 `temp/steward-design/real/` 给用户过目；提交。

## 验证

- 后端：`go test` 收窄到 `./internal/operations/...` 里受影响的用例、`./internal/catalog/governance/stewardtools/...`、
  `./internal/steward/...` 里新增与受影响的用例、迁移用例。治理 Executor 本身没改的用例不重跑。
- 前端：`tsc --noEmit`、改动文件 lint、`steward*` 与操作记录页的测试、新增的端到端用例。

## 风险点

- `operations.Run` 是全站共用的写入入口：只加可选字段，现有调用方与指纹不变；用现有治理用例里直接相关的几条核对。
- 重放逻辑改动会影响所有会话：C2 的重放回归用例属于受影响范围，要跑。
- 回退点：迁移 0054 带 down；工具注册可以整体摘掉。

## 执行记录（2026-11-16）

- 实现：后端由 Codex 子代理完成，前端由 Opus 子代理完成；测试由另外的 Codex 子代理编写与运行。
- 后端测试第一轮（隔离数据库）：97 个叶用例通过、4 个失败，均因 `revert_operation` 输入 schema 不是合法 JSON 导致装配 panic；已修。
- 独立只读审查：无阻断；应修 3 条（新消息静默续跑已批准项、待确认时取消报成存储错误、规范不一致）与若干小项均已修。
- 后端测试第二轮：48 个叶用例通过、0 失败；上一轮 4 个失败用例通过；API 样本由真实 handler 重新生成（10 份有差异）。上一轮通过且未改的用例未重跑（含 AutoApprovalSessionScope，免确认新规则由 D7 用例覆盖）。
- 前端（子代理汇报）：tsc、eslint 通过；Vitest 第一轮 14 个文件 363 例、追加修改后 11 个文件 219 例通过。
- 主代理实际运行：`e2e/steward-plan.spec.ts` 2 例通过（Playwright 容器）；`src/test/api-contract.test.ts` 644 例通过、1 例按条件跳过（与本任务无关的大样本用例）。
- 截图：`temp/steward-design/real/real-c6-*.png`。
- 未做：全量后端与前端测试、真实模型、真实 MCP 客户端、修复后的再次独立审查。
- 决定 D7（主代理）：不可撤销工具不提供“这次对话里不再问我”，待用户过目。
