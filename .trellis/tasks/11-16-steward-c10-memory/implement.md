# 管家 C10 执行计划

分支 `steward-c10-memory`，依赖 C6–C9 已合入。后端实现 `gpt-6-astra` high，测试由另一个子代理；前端 Opus 5.5（high）。

- [ ] 1 后端：Supervisor 与运行解耦、接回接口、取消钩子、关闭收尾、未读标记。
- [ ] 2 后端：迁移 0057、记忆（工具、接口、提示词）、历史摘要。
- [ ] 3 后端测试；规范同步（运行时合同第 4、5、7、9 节）；ADR 0005 第 6 节补摘要与记忆进提示词的说明。
- [ ] 4 前端：接回、标记、提示点、记忆设置；组件测试与端到端（刷新后接回）。
- [ ] 5 独立审查（只读，重点是并发、跨用户隔离与取消路径），修复，提交。

## 验证

- 后端：`go test ./internal/steward/runtime/... ./internal/steward/httpapi/... ./internal/steward/memory/...`；
  解耦改的是所有运行都经过的路径，`runtime` 与 `httpapi` 两个包的用例整包跑（理由：影响无法局部覆盖）。
  有 CGO 的环境里对 Supervisor 的单元测试加 `-race`；没有则记为未执行。
- 前端：`tsc --noEmit`、改动文件 lint、`src/features/steward/run`、`steward*`、`agent-settings`、端到端新增用例。

## 风险点

- 解耦改变了取消语义：遗漏任何一个取消钩子都会留下没人管的运行，逐项列进测试。
- 内存里的事件日志有上限与释放时间，避免长运行占用过多内存。
