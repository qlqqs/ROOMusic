# 实施计划：外部接口 mock 服务器

分支 `feat/m8-mock-upstream`，独立工作目录 `~/workspace/.worktrees/ROOMusic/m8-mock-upstream`。合入本地 `main`，不推送。

## 顺序

1. 实现代理（gpt-6.2-astra，high）：
   - `internal/upstreammock` 与 `cmd/roomusic-upstream-mock`：路由、情景、控制接口、请求记录、内置数据、四个外部源、SearXNG、Tavily。
   - 模型端点三种协议的流式响应与脚本；对照 `provider/` 的解码器与 `probe.go`。
   - 后端开关：config、app 组装、出站策略放行、Tavily 地址可配置。
   - `Makefile` 的 `mock-upstream` 目标；`docs/development-environment.md` 增加“外部接口 mock”一节；
     `.trellis/spec/backend/sources-contracts.md` 与 `deployment-environment-contracts.md` 记下变量与放行规则。
   - 实现代理不写新测试；只跑编译、`go vet` 与改动包的既有测试。
2. 测试代理（gpt-6.2-sol，high）：
   - `internal/upstreammock` 的单元测试：每个服务的路由、每种情景、控制接口、请求记录脱敏、`-data` 覆盖。
   - config 的变量校验；app 组装在设与不设变量时的基础地址与出站策略（A2）。
   - 端到端（进程内启动 mock）：四个外部源客户端对 `ok`、`unavailable`、`rate_limited`、`timeout`、`malformed`、`drop` 的结果符合 `sources-contracts.md`；
     三种模型协议通过端点检测并完成一次工具调用；SearXNG、Tavily 返回结果（A3–A6）。
3. 主代理验收：审改动范围；在宿主机启动 mock 与开发后端（数据库用一次性的测试 Compose 项目），打开自动整理扫描一个带合成 MBID 标签的小曲库，
   看请求记录（A1）；切几个情景看设置页的源状态与整理进度。

## 验证命令

```bash
cd backend
mise exec -- go build ./...
mise exec -- go vet ./internal/upstreammock/... ./cmd/roomusic-upstream-mock/... ./internal/platform/config/... ./internal/app/... ./internal/sources/... ./internal/steward/web/...
mise exec -- go test -race ./internal/upstreammock/... ./internal/platform/config/... ./internal/sources/... ./internal/steward/web/... ./internal/steward/provider/...
mise exec -- go test -race -run 'Layout|Depend' ./...   # 依赖规则测试覆盖新包
```

app 组装若有需要 PostgreSQL 的测试，按 `docs/development-environment.md` 的隔离做法单独跑，只跑相关用例。

## 回退点

- 后端开关是独立提交，可单独回退；mock 程序与后端互不依赖编译。
