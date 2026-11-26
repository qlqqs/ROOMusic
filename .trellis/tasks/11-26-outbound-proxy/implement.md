# 实施计划

## 步骤

1. 用 Orca 建工作目录 `task/outbound-proxy`，基于 `main`。
2. 后端实现交给 Codex（`gpt-6.2-astra`），内容：
   - 迁移；
   - 设置存储与接口；
   - `egress` 代理与错误分类；
   - 启动加载、保存后生效；
   - 诊断脱敏。

   实现代理不写新测试。
3. 后端测试交给另一个 Codex（`gpt-6.2-sol`），内容：
   - `egress` 单元测试：用进程内的 HTTP CONNECT 与 SOCKS5 测试代理计数，覆盖有认证和无认证；
   - 接口集成测试；
   - V1–V7。
4. 前端由主代理在同一工作目录实现：“网络”栏目、解码器、请求、单元测试。
5. 文档：
   - 新增 `.trellis/spec/backend/outbound-proxy-contracts.md`，或者并进 `sources-contracts.md` 的出站一节；
   - 更新 `docs/development-environment.md` 的出站说明。
6. 验收后提交，合入本地 `main`。

## 验证命令

- `cd backend && mise exec -- go test ./internal/platform/egress/... ./internal/sources/... -count=1`
- 集成测试按用例名收窄：`ROOMUSIC_TEST_RUN="^TestPostgreSQL(OutboundProxy.*)$"`，使用独立临时目录和唯一项目名。
- 前端：`cd frontend && mise exec -- npx vitest run src/app/network-settings.test.tsx`，以及 `npx tsc --noEmit`。

## 回滚

单个合并提交加一个迁移，可以整体 revert，`down` 迁移会删除新表。
