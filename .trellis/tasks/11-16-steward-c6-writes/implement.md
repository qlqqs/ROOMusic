# 管家 C6 执行计划

分支 `steward-c6-writes`，依赖 C5 已合入。后端实现 `gpt-6-astra` high，测试由另一个子代理；前端 Opus 5.5（high）。

- [ ] 1 后端：迁移 0054（来源列、方案表、调用状态、会话列）；`operations.Spec.Origin`；`Preview`；治理工具与撤销工具。
- [ ] 2 后端：调用入口的 `shared` 路径、方案生成与挂起、确认接口、模式、MCP 直接执行。
- [ ] 3 后端测试与样本；规范同步（运行时合同、治理合同、`agent-and-operation-guidelines.md` 的 operator 条文与错误码）。
- [ ] 4 前端：方案卡片、结果与撤销、模式按钮、操作记录来源；组件测试与一条端到端冒烟。
- [ ] 5 独立审查（只读），修复；截图存 `temp/steward-design/real/` 给用户过目；提交。

## 验证

- 后端：`go test` 收窄到 `./internal/operations/...` 里受影响的用例、`./internal/catalog/governance/stewardtools/...`、
  `./internal/steward/...` 里新增与受影响的用例、迁移用例。治理 Executor 本身没改的用例不重跑。
- 前端：`tsc --noEmit`、改动文件 lint、`steward*` 与操作记录页的测试、新增的端到端用例。

## 风险点

- `operations.Run` 是全站共用的写入入口：只加可选字段，现有调用方与指纹不变；用现有治理用例里直接相关的几条核对。
- 重放逻辑改动会影响所有会话：C2 的重放回归用例属于受影响范围，要跑。
- 回退点：迁移 0054 带 down；工具注册可以整体摘掉。
