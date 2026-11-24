# 实施计划：匿名实例统计与源代码链接

工作目录：`~/workspace/.worktrees/ROOMusic/instance-insights`，分支 `feat/instance-insights`（从 `main` 建）。

1. 后端实现（Codex，gpt-6.2-astra）：迁移、`internal/insights`（schema、客户端、调度、接口）、`collector`、`cmd/roomusic-insights`、配置项、mock 服务、`system` 的源代码字段、接口样本、`deploy/insights/`。不写新测试。
2. 后端测试（Codex，gpt-6.2-sol，另一个代理）：schema 校验、分档、调度时机（24 小时、开关、部署层关闭、开发版本）、发送内容与预览一致、出站规则、迁移往返、接口集成测试、统计端（校验、限速、去重、窗口、原子写、清理、日志无 IP）。
3. 前端（主代理）：解码器、概览说明、设置区段、关于页、登录页链接；单测；390/1440 截图。
4. 文档（主代理）：ADR 0008、`docs/insights.md`、`.trellis/spec/backend/insights-contracts.md`、部署说明与 README、`architecture-decisions.md` 的 ADR 索引。
5. 审阅、验收、合入本地 `main`。

## 验证命令（范围上限）

- `cd backend && mise exec -- go test ./internal/insights/... ./cmd/roomusic-insights/... ./internal/upstreammock/...`
- 集成：`ROOMUSIC_TEST_RUN='Insights|SystemVersion|APISamples' sg docker -c ./scripts/test-integration.sh`
- `cd frontend && mise exec -- npm run lint && npm run typecheck && npx vitest run <相关文件>`
- 统计端冒烟：本地启动 `roomusic-insights`，用 curl 发合法/非法心跳，读汇总。

## 回退点

- 迁移 0072 可下行；功能整体由开关与部署层关闭控制。
