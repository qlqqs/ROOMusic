# 管家 C5 执行计划

分支 `steward-c5-personal`。后端实现交 Codex（`gpt-6-astra` high），测试交另一个子代理（`gpt-6.1-sol` high）；
前端交 Opus 5.5（high）子代理；子代理不提交 Git。

- [ ] 1 后端：调用入口放开 `personal`；三个 `stewardtools` 包与注册；迁移 0053；上下文的输入、落库与重放。
- [ ] 2 后端测试与接口样本；规范 `steward-runtime-contracts.md` 第 2–5、7、9 节同步。
- [ ] 3 前端：上下文标签与请求体、工具标签、查询失效；组件测试。
- [ ] 4 独立审查（trellis-check，只读），修复后提交。

## 验证

- 后端：`go test` 收窄到 `./internal/steward/tool/...`、三个 `stewardtools` 包、`./internal/steward/runtime/...`、
  `./internal/steward/httpapi/...` 与 `./internal/steward/mcp/...` 里新增和受影响的用例，以及迁移与依赖规则用例。
- 前端：`tsc --noEmit`、改动文件 lint、`steward*` 测试文件。
- 集成测试用独立 Compose 项目名与合成数据，模型端点用本地假服务。

## 风险点

- `zones.Service.Command` 不幂等、不重试：工具失败时不自动重试。
- `app/steward.go`、依赖规则测试是共享文件，只追加。
