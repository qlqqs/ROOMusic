# M5 ① 历史页面：执行计划

需求见 [prd.md](prd.md)，接口合同见 [design.md](design.md)。分工按 `~/.claude/CLAUDE.md`：后端实现与所有测试由 GPT-6-Astra，前端实现由 Opus 5.5；子 agent 不提交，主会话审阅后按步骤提交。数据库测试只用隔离的 `roomusic-test` Compose 项目，结束 `down -v`。

## 步骤

| 步骤 | 执行者 | 内容 | 验证 | 提交 |
| --- | --- | --- | --- | --- |
| 1 后端 | Astra | catalog `LoadTrackSummaries`（含 missing、来源事实、1,000 上限）；listening 汇总读取、queue、按曲目删除与 httpapi 三个路由；`app` 装配；接口样本 9 个；隔离库上 `EXPLAIN (ANALYZE, BUFFERS)` 记录结论（需要索引才加迁移 0021）；同步 `listening-contracts.md` 与 `catalog-rest-contracts.md` | `go test -race` catalog、listening、app（集成测试 0 SKIP）；`go vet`；依赖规则测试；样本由真实服务生成 | `feat(listening): 按曲目汇总的历史接口` |
| 2 前端 | Opus（可与 1 并行，按 design.md 合同） | 导航“我的音乐”；`/me` 布局与页签、`/me/history` 页；listening 的解码器与查询键（`tracks`、`queue`，上报、移除、清除后失效）；行内播放/更多菜单/播放全部由 `app` 层组合 player 动作；移除与清除确认；缺失行；概览“全部历史”链接；同步前端规范 | lint、typecheck、build（重建 `backend/cmd/roomusic/web`） | 等 3 一起提交 |
| 3 前端测试 | Astra | 解码器对步骤 1 样本往返；组件与页面 Vitest（状态、分页地址、缺失行、移除与清除流程、播放全部的计数与提示、队列满）；Playwright（五种宽度、axe、纯键盘播放/移除/清除）；真实服务用例（播放过半后历史页出现、次数 1，移除后消失） | `npx vitest run`、`npx playwright test`、`scripts/playback-e2e.sh` | 与 2 合为 `feat(frontend): 我的音乐的历史页签` |
| 4 收尾 | Astra + 主会话 | 导出树验证一次（前后端相关测试、Playwright、真实服务用例）；1440 与 375 截图（合成数据，临时 spec 不入库），主会话复核布局 | 同上 | 结果写入 PRD → 归档 → 链接修正与 journal 合一提交 |

## 注意

- 改行为合同时，搜索 `frontend/e2e/real/` 与 `scripts/playback-e2e.sh` 里依赖旧行为的断言（例如“概览没有全部历史链接”）。
- 前端 listening 不导入 player（现有 player→listening 依赖，避免成环），播放动作在 `app` 层组合。
- 回滚点：步骤 1 与步骤 2+3 各是独立提交；前端提交连同重建的内嵌产物。
