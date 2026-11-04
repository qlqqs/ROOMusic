# M5 ⑥ 扫描反馈：执行计划

需求见 [prd.md](prd.md)，合同见 [design.md](design.md)。属于父任务的 **F 线**（[父任务](../11-04-classical-m5/prd.md)“一次规划、并行实现”），合入顺序第 2（P 线之后）。分工按 `~/.claude/CLAUDE.md`：后端与所有测试由 GPT-6-Astra，前端由 Opus 5.5；子 agent 不提交，主会话审阅后在本线 worktree 里按步骤提交。

## 并行环境

- 工作目录：独立 git worktree，分支 `m5-scan-feedback`，基于当前 `main`。
- 隔离库：Compose 项目名 `roomusic-test-m5f`（`docker compose --env-file /dev/null -f <绝对路径>/compose.test.yaml -p roomusic-test-m5f`），空闲端口、随机库名，用完 `down -v`；不得碰用户的 `roomusic-postgres-1`。
- Playwright（固定端口 4173）与 `scripts/playback-e2e.sh`（独占 `roomusic-test` 项目）三条线共用，必须用 `flock /tmp/roomusic-m5-e2e.lock <命令>` 串行执行。
- 迁移：在本线基线上用下一个空号；合入 `main` 时由主会话按合入顺序改号并重跑迁移与集成测试。

## 步骤

| 步骤 | 执行者 | 内容 | 验证 | 提交 |
| --- | --- | --- | --- | --- |
| 1 后端 | Astra | design.md §2–§6：全根计数、逐根流水与加权单调进度（分批持久化、终态冻结）；扫描变更捕获与报告计数（§4.2–4.4，含 `vacateSourceIdentity`、`MarkMissingSources` 分块记账）；新增专辑记录与实时“可能接替了《…》”（§4.5）；`GET /release-groups/recently-added` 与 `sort=added_at_desc`、`added_at` 字段（§5）；`ScanDTO` 增量字段与管理员报告接口（§6）；§10.3 锁顺序与治理并发回归矩阵；接口样本（§8.2）；同步 `scan-coordination-contracts.md`、`library-scan-contracts.md`、`catalog-rest-contracts.md`、`governance-contracts.md` 的锁合同 | `go test -race ./internal/library/... ./internal/catalog/... ./internal/app -count=1`（集成 0 SKIP）；`go vet` 同范围；依赖规则测试；样本由真实服务生成；`EXPLAIN (ANALYZE, BUFFERS)` 记录最近新增与排序的计划 | `feat(library): 扫描进度、报告与最近新增` |
| 2 前端 | Opus（与 1 并行，按 design.md §7） | 概览与扫描详情的进度条（`role="progressbar"`）、阶段与“第 i / 共 R 个目录”；扫描详情的报告与新增专辑列表（替身标记链接到认领）；报告不可用时一句话；概览“最近新增”区块（无数据隐藏）；专辑库“最近新增”排序；改写 §7.3 列出的 spec/DESIGN 条目。界面文案遵守 `~/.claude/CLAUDE.md`“前端界面文案” | lint、typecheck、build（重建 `backend/cmd/roomusic/web`） | 等 3 一起提交 |
| 3 测试 | Astra | 解码器对样本往返；Vitest（进度单调显示、各终态、报告与不可用、最近新增隐藏、排序地址）；Playwright（五种宽度、axe、扫描中的样子）；真实服务用例：扫描一个小目录，进度到 100%，报告计数正确（design.md §8.4） | `npx vitest run`（相关文件）、`flock … npx playwright test <相关 spec>`、`flock … scripts/playback-e2e.sh` | 与 2 合为 `feat(frontend): 扫描进度、报告与最近新增` |
| 4 收尾 | Astra + 主会话 | 导出树验证一次；1440 与 375 截图（含扫描中，合成数据，临时 spec 不入库），主会话复核 | 同上 | 结果写入 PRD；合入 `main` 后归档 |

## 注意

- 现有文档“没有总数合同就不显示百分比”的条目由本线改写（design.md §7.3），不要只改代码。
- 与 S 线（⑤ 在 `ingest/persist.go` 同事务挂接艺人解析）冲突风险最高的文件：`library/scanner.go`、`catalog/ingest/persist.go`、`persist_track.go`；本线先合入，S 线变基时处理。
- 回滚点：步骤 1 与步骤 2+3 各是独立提交。
