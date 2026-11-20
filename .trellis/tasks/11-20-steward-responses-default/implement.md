# 执行计划

分支 `steward-responses-default`（从 `main` 建），在主工作目录进行。后端实现与测试分别派给 Codex（`gpt-6.1-sol`，high），不提交 Git；前端用例、规范与提交由主代理做。

## 步骤

1. [x] 后端实现（Codex 实现代理）：按 `design.md` 改 `backend/internal/steward/provider/probe.go`；只改这一个文件，不改测试。
   验证：`cd backend && mise exec -- go build ./... && mise exec -- go vet ./internal/steward/provider/`
2. [x] 后端测试（Codex 测试代理）：更新并补充 `backend/internal/steward/provider/` 下的探测用例（`protocol_test.go` 的
   `TestProbeProtocolsModelsAndThreeToolConclusions`、`c8_test.go` 的 `TestC8ProbeResponsesAndNativeSearch`），覆盖 A1、A2、A3 与三条调用次数路径；
   `backend/internal/steward/httpapi/` 里受影响的探测集成用例同步期望值。
   验证：`cd backend && mise exec -- go test ./internal/steward/provider/ -run "Probe" -count=1`；
   集成用例用 `ROOMUSIC_TEST_RUN` 收窄到名字含 `Probe` 的用例，经 `./scripts/test-integration.sh` 运行。
3. [x] 前端用例（主代理）：`frontend/src/app/agent-settings.test.tsx` 加一条“检测结论为 Responses 时不显示未确认提示”；发现显示不对再改组件。
   验证：`cd frontend && mise exec -- npx vitest run src/app/agent-settings.test.tsx`
4. [x] 规范同步（主代理）：`.trellis/spec/backend/steward-runtime-contracts.md` 第 6 节探测步骤，`.trellis/spec/backend/steward-web-contracts.md`
   第 53、94、235 行附近“默认仍为 openai”与探测顺序，`.trellis/spec/frontend/steward-guidelines.md` 第 299 行附近。
5. [x] 真实端点验收（主代理，A4）：用本分支重建试用实例 `temp/steward-trial-20261120`，对 `api.qlqq.de` 重新探测，
   再各跑一次只读对话、确认写入、steward 审查、自带搜索。
6. [ ] 审阅子代理改动范围，提交，合回 `main`，删除任务分支。

## 回滚点

第 1、2 步之后各有一次可独立还原的改动；没有迁移。
