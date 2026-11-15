# 执行计划

分支 `feat/queue-redesign`，在 `feat/player-controls-merge` 合入 main 后从 main 切出。

1. 后端实现（Codex `gpt-6-astra` high，不写新测试、不提交）：Q5 全部改动与契约文档。
   验证：`cd backend && mise exec -- go build ./... && mise exec -- go vet ./internal/zones/...`
2. 后端测试（另一个 Codex `gpt-6.1-sol` high）：`queue_test.go` 与 `control_integration_test.go` 覆盖首、尾、跨当前项、越界、空操作、持久化。
   验证：`cd backend && mise exec -- go test ./internal/zones/...`
3. 前端（主代理）：controller / decoders / zone-store；`player-queue.tsx` 拆分与拖拽；抽屉动画与窄屏面板；律动条。
4. 前端测试（子代理）：`player-ui.test.tsx` 队列部分、decoders 测试；e2e `player`、`zones-devices`、`responsive`、`a11y`。
   验证：`cd frontend && mise exec -- npm run typecheck && mise exec -- npm run lint && mise exec -- npx vitest run <受影响文件> && mise exec -- npx playwright test <受影响 spec>`
5. 真实服务两台设备用例（`scripts/playback-e2e.sh`）里加一次拖拽同步检查。
6. 规范更新；截图核对 1440 / 768 / 390 与减少动态效果。
