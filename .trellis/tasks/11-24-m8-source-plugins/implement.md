# 实施计划：第三方数据源插件

分支 `feat/m8-source-plugins`（基于合入 U3 之后的 `main`），独立工作目录 `~/workspace/.worktrees/ROOMusic/m8-source-plugins`。合入本地 `main`，不推送。

## 顺序

1. 主代理：写 ADR 0007（裁决与规则），修订 ADR 0006 U4 的标注与架构决策摘要“两步走”。
2. 后端实现代理（gpt-6.2-astra，high）：适配层、清单与包、宿主函数、插件即外部源、管理接口、迁移、示例插件、插件开发说明、规范 `.trellis/spec/backend/plugin-contracts.md`。
   先在生产镜像同样的限制下（只读根、`cap_drop: ALL`）确认 wazero 的运行模式，再定编译缓存位置。
3. 后端测试代理（gpt-6.2-sol，high）：A2–A6 的沙箱与出网用例（用专门写的恶意测试插件），A1 的端到端（示例插件 + mock + 隔离 PostgreSQL）。
4. 主代理：前端（插件卡片、安装确认、外部源里的插件行），前端测试代理补测试与 390/1440 截图。
5. 主代理验收：开发后端接 mock，安装示例插件，走 A1；Compose 测试项目里跑 A6。

## 验证命令

```bash
cd backend
mise exec -- go build ./... && mise exec -- go vet ./internal/plugins/... ./internal/app/...
(cd plugins/examples/lyrics-demo && GOOS=wasip1 GOARCH=wasm mise exec -- go build -o plugin.wasm .)
mise exec -- go test -race ./internal/plugins/...
# 数据库用例：ROOMUSIC_TEST_RUN='<相关用例>' ./scripts/test-integration.sh
cd ../frontend && mise exec -- npm run typecheck && mise exec -- npx vitest run <相关文件> && mise exec -- npx playwright test e2e/plugins*.spec.ts
```

## 回退点

- ADR、后端、前端分别提交；插件默认不启用，合入后不安装插件的实例行为不变。
