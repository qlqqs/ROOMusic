# 管家 C3 执行计划

前置：用户批准本任务的规划摘要后 `task.py start`，在分支 `steward-c3-mcp` 上做。
后端实现与测试交给不同的 Codex 子代理（复杂任务，`gpt-6-astra` high），子代理不提交 Git；前端由主代理实现。

## 步骤

- [x] 1 引入 SDK：`go.mod` 加 `github.com/modelcontextprotocol/go-sdk` v1.8.0，核对实际新增的模块与调研一致；依赖规则测试加条目。
- [x] 2 迁移 0052（含 down）与 `steward/token`。
- [x] 3 令牌管理接口（`steward/httpapi`）与成员开关的判断。
- [x] 4 `steward/mcp`：认证、失败限速与来源检查、按调用者构造 Server、定义与结果转换、日志；`app` 注册 `/api/v1/mcp`。
  - 评审点：主代理审阅认证与过滤的代码后再派测试。
- [x] 5 测试（另一个子代理）：令牌的单元测试；数据库集成测试覆盖 PRD 的 A1–A7 与 A10（MCP 一侧用 SDK 的客户端，新旧握手各一遍）；三条接口的响应样本。
- [x] 6 前端“访问令牌”栏目与组件测试（主代理）。
- [x] 7 文档：`steward-runtime-contracts.md` 加 MCP 与令牌的合同；`docs` 里加一段接入 Claude Code 的说明；父任务执行计划勾选。
- [ ] 8 真实客户端试用由用户执行，结果单独记录。

## 执行记录（2026-11-16）

- 第 1–4 步由实现子代理完成。`go.mod` 新增 SDK 与 6 个间接依赖，现有依赖版本未变。主代理审阅认证、过滤与结果转换的代码后追加一处修复：
  工具处理函数 panic 时在 MCP 入口恢复并记 `steward.mcp.tool.panicked`。主代理运行 `go build ./...` 与相关包 `go vet` 通过。
- 第 5 步由另一个子代理完成，报告新增用例全部通过、无跳过、未发现实现缺陷，三份样本已生成，测试用的 Compose 资源已清理
  （主代理另查无残留）。主代理读过 MCP 集成测试的断言，没有重跑。未跑 `-race`。
- 第 6 步：主代理运行类型检查、改动文件的 lint，以及 `tokens.test.tsx`、`settings.test.tsx`、`settings-sections.test.ts`、
  `api-contract.test.ts`，全部通过。端到端用例只改了 `e2e/auth.spec.ts` 里的栏目清单，没有运行。
- 契约测试里补登记了 C2 的 30 个管家样本：C2 合入时没有登记，这个测试在此之前是失败的。


## 验证

命令只覆盖本任务改动的包：`mise exec -- go test ./internal/steward/token/... ./internal/steward/mcp/... ./internal/steward/httpapi/... ./internal/app/...`
（后两个用 `-run` 收窄到令牌、MCP、路由、依赖规则与样本的用例）；前端只跑新增栏目的测试、类型检查与 lint。
集成测试用独立临时目录、唯一 Compose 项目名和合成数据。C2 已通过且未改动的用例不重跑。

## 风险点

- `go.mod`、`go.sum` 是共享文件，改动限于新增 SDK 及其依赖；现有依赖的版本如果被连带抬高，停下来交主代理确认。
- `identity.Guard` 是全站共用的，只新增方法，不改现有方法的行为。
- `Registry.Invoke` 目前只放行 `read`，本任务不改它。

## 启动前检查

- [x] 用户批准本任务的最终规划摘要（2026-11-16）。
- [x] `implement.jsonl`、`check.jsonl` 已有条目。
