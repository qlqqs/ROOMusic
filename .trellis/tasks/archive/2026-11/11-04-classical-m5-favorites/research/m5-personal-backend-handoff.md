# M5 P 线步骤 1 后端交付与追加验收

日期：2026-11-04。仅工作于 `m5-personal` worktree；未 commit/stash/reset/checkout，未修改并行前端、内嵌web产物或其他worktree。任务还有后续前端/组合验收，未归档。本文采用**本轮最终重跑结果**；此前877项记录保留在 [backend-verification.json](backend-verification.json)，不混算。

## 本轮完成情况

1. 三份阶段性报告已由 `docs/architecture/` 移至当前任务 `research/`，所有相对链接已校验。长期架构目录不再含这三个文件；[design §10](../design.md#10-性能预期与必须验证的-explain)末尾仅保留结论、关键数字及报告链接。
2. 新真实形状100,000轨/10,000发行/8,000组、每发行10轨、5%missing；20张各10,000首卡片、20,000收藏、10,000首歌单，均保留另一用户干扰。空辅助表未ANALYZE/ANALYZE两种状态分别验收，未缩减规模。
3. 服务端及完整TCP的p95全部通过，20样本×8场景×2阶段=320有效样本；已重跑指定race、vet、依赖规则和全部API样本回放。
4. 最新JIT许可已写入 [database-guidelines.md](../../../../../spec/backend/database-guidelines.md#延迟敏感只读事务与-jit)：只在延迟敏感只读事务BEGIN后使用SET LOCAL，失败回滚；同PID提交/回滚恢复默认值；禁止其他planner参数、会话/全局改值及业务ANALYZE。F/S线可引用。

## 优化、功能与外部合同

- 保留已审阅的收藏/歌单CRUD、owner隔离、分页/lookup、完整队列计数、间隔键、revision 409、幂等收据、适应撤销与超限等合同；未实施⑤艺人收藏。
- 新catalog公开 `LoadTrackCollectionStats`，每批≤10,000 UUID，只聚合匹配/missing/时长/未知数；卡片继续遍历完整引用，封面位置facts按32起指数窗口补前四组，四组齐全后不再补位置。不读取catalog私表、不截断超限统计、不生成逐曲完整摘要。
- personal只读RR事务执行 `SET LOCAL jit = off`；设置失败按查询错误终止，写事务不变。实际Service入口和同一连接/PID生命周期、取消/SET失败/回滚均有测试。
- 不引入缓存、派生持久统计、200,000输入API或新Go依赖。HTTP路径、字段、状态码无变化。内部统计的浮点SUM遵守正有限值合同，小数等价测试使用容差，不承诺与逐项Go累加逐位相同。
- 本轮未改迁移；步骤1仍为 `backend/migrations/0021_favorites.sql`、`0022_playlists.sql`，合入时由主会话决定编号。
- 已撤回的一次性20万聚合/全组排序/窗口去重facts方案仅保留实验JSON，不在生产代码。

## 两组性能数字

### 当前真实形状：全部达标

单位ms，表内为**完整TCP p95 / 服务端handler p95**；两个阶段均每场景20个有效样本。

| 场景 | 空辅助表未ANALYZE | 辅助表ANALYZE后 | 门槛 |
| --- | ---: | ---: | ---: |
| 20张万首卡片 | 841.08 / 840.53 | 863.15 / 862.77 | 1,000 |
| 2万收藏播放前10,000可用轨 | 642.92 / 625.43 | 660.75 / 634.10 | 2,000 |
| 万首歌单播放全部可用轨 | 558.01 / 545.32 | 540.25 / 527.33 | 2,000 |
| 收藏曲目首50项 | 14.23 / 13.70 | 14.26 / 13.78 | 300 |
| 收藏专辑首50项 | 15.49 / 14.98 | 14.12 / 13.83 | 300 |
| 歌单摘要+首50项串行组合 | 60.91 / 60.12 | 62.95 / 62.00 | 300 |

[个人性能报告](m5-personal-performance.md)与 [final.json](realistic-performance/final.json)含全部8场景、原始样本、环境、响应大小和统计前后验证。收藏/歌单队列响应约5.62/5.34MB；不把Go解码时间称为浏览器性能，未测严格冷缓存或并发压测。

### 极端历史形状：保留风险，不追目标

单发行十万轨、80%missing，上一轮实现的**服务+JSON**（5样本、非完整HTTP）：

| 场景 | 历史p50 ms | 历史p95 ms | 与本次正常形状门槛比较 |
| --- | ---: | ---: | --- |
| 20张万首卡片 | 3331.96 | 3408.44 | 超过1s |
| 2万收藏队列 | 10513.35 | 10956.35 | 超过2s |
| 万首歌单队列 | 5443.06 | 5574.01 | 超过2s |
| 十万收藏队列 | 27105.31 | 27609.56 | 扩展风险项，未设本轮门槛 |

这不是当前实现的完整HTTP复测，不能据此作新旧同口径倍数比较；用户明确不追该极端形状。`personal-explain/` 的测量仍保留在仓库；catalog原始计划未入库，本机保存在 `/home/qlqq/.local/share/roomusic-evidence/m5-personal/catalog-explain/`，可用[Catalog报告](m5-personal-catalog-explain.md)中的命令重新生成当前实现的计划。范围race保留极端正确性检查，不用race数字更新性能结论。

### EXPLAIN结论

本轮28份实际SQL计划见 [Catalog报告](m5-personal-catalog-explain.md)：10,000统计聚合热测10.710/10.415ms，tracks主键受请求集限定，估算/实际10,000、loops=1，无JIT或temp；32项封面位置facts为0.248/0.333ms。空辅助表下千发行完整摘要默认JIT编译106.944ms、总执行143.047ms，事务局部off后37.505ms；相同读取工作量，不改变planner开关。辅助表ANALYZE后同查询默认不触发JIT，39.966ms。

首轮间隔键重整fresh退化修复仍保留（约110s→0.30s），此次未改该实现。

## 本轮验证与样本

最终指定范围 **315个顶层测试、含子测试930项通过，0 FAIL、0 SKIP**；源码集成分类与实际pass交集为 **223个顶层、含子测试591项**。10个包全部通过。机器可读命令、时间、集成清单、样本SHA与清理状态见 [backend-followup-verification.json](backend-followup-verification.json)。

实际执行（`backend` 下；本次隔离URL、`ROOMUSIC_CATALOG_EXPLAIN=1`、固定构建的 `ROOMUSIC_TEST_FFMPEG`，无样本更新开关，无性能报告输出目录）：

```bash
go test -race ./internal/personal/... ./internal/catalog/... ./internal/app -count=1 -json
go vet ./internal/personal/... ./internal/catalog/... ./internal/app
go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1 -json
go test ./internal/app -run '^TestPostgreSQL(APIResponseSamples|PersonalAPIResponseSamples)$' -count=1 -json
go build ./internal/personal/... ./internal/catalog/... ./internal/app
```

- 两组真实服务样本回放通过。全部219份golden前后SHA-256一致，包含personal70份；路径为 `backend/internal/app/testdata/api-samples/{favorites,playlists}/`、`errors/personal-*.json`，无重新生成或形状变化。
- 初次race发现新增1,001轨跨批摘要夹具重复非空 `source_identity`，被23505唯一约束正确拒绝。修正为同root/path的已解绑来源（NULL身份）；定向race通过后，**整个指定范围重新执行**，不排除失败用例。生产代码未改变，不需要重做正式HTTP测量。该构造约束已补入catalog测试规范。
- 新聚合覆盖有效小数、NaN/±Inf/非正/NULL、SUM溢出、未知身份、输入上限、RR快照、取消与单连接释放；封面覆盖指数窗口、10,000/10,001第四组边界、无封面/missing和四组齐全后仍完整检查身份；JIT覆盖失败阻断、实际事务、同PID默认值恢复。
- gofmt、定向diff空白检查、相对链接、spec文件<24KB、无Go依赖/迁移变化及报告JSON/p95重算检查通过。
- 未运行全仓或前端测试，遵守用户的相关最小范围；前端/Playwright/浏览器decoder/DOM留步骤3/4，不代替其验收。

## 隔离库与收尾

本轮唯一自有 `roomusic-test-m5p`，随机库名、空闲端口，未使用项目.env或用户业务库。完成后核对默认 `jit=on`、残留测试schema=0；独占启动器执行以下命令，验证本项目容器/卷/网络均为0，并删除本次 `/tmp/roomusic-m5p-test-env`。没有操作 `roomusic-postgres-1`、`roomusic`、共享 `roomusic-test` 或其他实现线库。

```bash
docker compose --env-file /dev/null -f /home/qlqq/.paseo/worktrees/2i6geckb/m5-personal/compose.test.yaml -p roomusic-test-m5p down -v
```

无外部合同偏差、无待主会话裁决的问题；仅严格冷缓存、非空巨量辅助表、并发及浏览器性能不作本轮保证。未提交Git，等待主会话审阅。

## 本轮改动文件（相对于已审阅步骤1）

### 后端源码、测试与独立夹具（16个）

- `backend/internal/app/personal_realistic_performance_integration_test.go`
- `backend/internal/app/testdata/personal-realistic-scale.sql`
- `backend/internal/catalog/track_collection_stats.go`
- `backend/internal/catalog/track_collection_stats_explain_integration_test.go`
- `backend/internal/catalog/track_collection_stats_integration_test.go`
- `backend/internal/catalog/track_collection_stats_test.go`
- `backend/internal/catalog/track_summary_chunk_parity_integration_test.go`
- `backend/internal/personal/performance_integration_test.go`
- `backend/internal/personal/playlist_reads.go`
- `backend/internal/personal/playlist_reads_integration_test.go`
- `backend/internal/personal/playlist_statistics.go`
- `backend/internal/personal/playlist_statistics_test.go`
- `backend/internal/personal/projection_test.go`
- `backend/internal/personal/read_tx_integration_test.go`
- `backend/internal/personal/read_tx_test.go`
- `backend/internal/personal/service.go`

### 文档与证据

- `.trellis/spec/backend/database-guidelines.md`
- `.trellis/spec/backend/catalog-rest-contracts.md`
- `.trellis/spec/backend/personal-contracts.md`
- `.trellis/tasks/11-04-classical-m5-favorites/design.md`
- `.trellis/tasks/11-04-classical-m5-favorites/implement.md`
- `.trellis/tasks/11-04-classical-m5-favorites/research/m5-personal-backend-handoff.md`
- `.trellis/tasks/11-04-classical-m5-favorites/research/m5-personal-performance.md`
- `.trellis/tasks/11-04-classical-m5-favorites/research/m5-personal-catalog-explain.md`
- `research/backend-followup-verification.json`：本轮最终门禁、集成测试清单及219份样本SHA。
- `research/realistic-performance/final.json`：当前正式HTTP验收。
- 本轮28份原始计划未入库，本机保存在 `/home/qlqq/.local/share/roomusic-evidence/m5-personal/realistic-performance/catalog-explain/realistic-cards/`，可用上文所引Catalog报告的命令重新生成。
- 基线及已撤回方案的探索JSON未入库，本机保存在 `/home/qlqq/.local/share/roomusic-evidence/m5-personal/realistic-performance/`；不冒称正式结果。仓库内该目录只保留 `final.json`。
- 验证JSON保持原始验收记录；其中指向 `catalog-explain/` 及已移出 `realistic-performance/` 内容的相对路径，现相对于上述仓库外证据根目录。

## 步骤1累计文件清单

以下排除并行前端及web产物；样本仍为首轮70份，本轮字节未变。上述研究证据目录另列，不将每份计划重复展开。

- `.trellis/spec/backend/catalog-rest-contracts.md`
- `.trellis/spec/backend/database-guidelines.md`
- `.trellis/spec/backend/directory-structure.md`
- `.trellis/spec/backend/index.md`
- `.trellis/spec/backend/listening-contracts.md`
- `.trellis/spec/backend/logging-guidelines.md`
- `.trellis/spec/backend/personal-contracts.md`
- `.trellis/tasks/11-04-classical-m5-favorites/design.md`
- `.trellis/tasks/11-04-classical-m5-favorites/implement.md`
- `.trellis/tasks/11-04-classical-m5-favorites/research/m5-personal-backend-handoff.md`
- `.trellis/tasks/11-04-classical-m5-favorites/research/m5-personal-catalog-explain.md`
- `.trellis/tasks/11-04-classical-m5-favorites/research/m5-personal-performance.md`
- `backend/internal/app/api_samples_integration_test.go`
- `backend/internal/app/app.go`
- `backend/internal/app/dependency_rules_test.go`
- `backend/internal/app/personal_api_integration_test.go`
- `backend/internal/app/personal_performance_integration_test.go`
- `backend/internal/app/personal_realistic_performance_integration_test.go`
- `backend/internal/app/personal_samples_integration_test.go`
- `backend/internal/app/personal_test.go`
- `backend/internal/app/testdata/api-samples/errors/personal-database-unavailable.json`
- `backend/internal/app/testdata/api-samples/errors/personal-favorites-changed.json`
- `backend/internal/app/testdata/api-samples/errors/personal-idempotency-conflict.json`
- `backend/internal/app/testdata/api-samples/errors/personal-invalid-input.json`
- `backend/internal/app/testdata/api-samples/errors/personal-invalid-json.json`
- `backend/internal/app/testdata/api-samples/errors/personal-invalid-pagination.json`
- `backend/internal/app/testdata/api-samples/errors/personal-method-not-allowed.json`
- `backend/internal/app/testdata/api-samples/errors/personal-not-found.json`
- `backend/internal/app/testdata/api-samples/errors/personal-origin-forbidden.json`
- `backend/internal/app/testdata/api-samples/errors/personal-origin-required.json`
- `backend/internal/app/testdata/api-samples/errors/personal-playlist-limit.json`
- `backend/internal/app/testdata/api-samples/errors/personal-playlist-track-limit.json`
- `backend/internal/app/testdata/api-samples/errors/personal-revision-conflict.json`
- `backend/internal/app/testdata/api-samples/errors/personal-unauthorized.json`
- `backend/internal/app/testdata/api-samples/favorites/album-delete.json`
- `backend/internal/app/testdata/api-samples/favorites/album-put.json`
- `backend/internal/app/testdata/api-samples/favorites/albums-empty.json`
- `backend/internal/app/testdata/api-samples/favorites/albums-unavailable.json`
- `backend/internal/app/testdata/api-samples/favorites/albums.json`
- `backend/internal/app/testdata/api-samples/favorites/lookup-empty.json`
- `backend/internal/app/testdata/api-samples/favorites/lookup.json`
- `backend/internal/app/testdata/api-samples/favorites/queue-empty.json`
- `backend/internal/app/testdata/api-samples/favorites/queue-missing.json`
- `backend/internal/app/testdata/api-samples/favorites/queue.json`
- `backend/internal/app/testdata/api-samples/favorites/track-delete-repeated.json`
- `backend/internal/app/testdata/api-samples/favorites/track-delete.json`
- `backend/internal/app/testdata/api-samples/favorites/track-put-repeated.json`
- `backend/internal/app/testdata/api-samples/favorites/track-put.json`
- `backend/internal/app/testdata/api-samples/favorites/tracks-empty.json`
- `backend/internal/app/testdata/api-samples/favorites/tracks-missing.json`
- `backend/internal/app/testdata/api-samples/favorites/tracks-out-of-range.json`
- `backend/internal/app/testdata/api-samples/favorites/tracks.json`
- `backend/internal/app/testdata/api-samples/playlists/add-items-all-existing.json`
- `backend/internal/app/testdata/api-samples/playlists/add-items-input-duplicates.json`
- `backend/internal/app/testdata/api-samples/playlists/add-items.json`
- `backend/internal/app/testdata/api-samples/playlists/containing-track-empty.json`
- `backend/internal/app/testdata/api-samples/playlists/containing-track.json`
- `backend/internal/app/testdata/api-samples/playlists/create-replayed.json`
- `backend/internal/app/testdata/api-samples/playlists/create-with-tracks.json`
- `backend/internal/app/testdata/api-samples/playlists/create.json`
- `backend/internal/app/testdata/api-samples/playlists/delete-replayed.json`
- `backend/internal/app/testdata/api-samples/playlists/delete.json`
- `backend/internal/app/testdata/api-samples/playlists/detail-over-limit.json`
- `backend/internal/app/testdata/api-samples/playlists/detail.json`
- `backend/internal/app/testdata/api-samples/playlists/items-empty.json`
- `backend/internal/app/testdata/api-samples/playlists/items-missing.json`
- `backend/internal/app/testdata/api-samples/playlists/items-next-page.json`
- `backend/internal/app/testdata/api-samples/playlists/items-out-of-range.json`
- `backend/internal/app/testdata/api-samples/playlists/items.json`
- `backend/internal/app/testdata/api-samples/playlists/list-empty.json`
- `backend/internal/app/testdata/api-samples/playlists/list-unknown-duration.json`
- `backend/internal/app/testdata/api-samples/playlists/list.json`
- `backend/internal/app/testdata/api-samples/playlists/move-before.json`
- `backend/internal/app/testdata/api-samples/playlists/move-down.json`
- `backend/internal/app/testdata/api-samples/playlists/move-first.json`
- `backend/internal/app/testdata/api-samples/playlists/move-last.json`
- `backend/internal/app/testdata/api-samples/playlists/move-noop.json`
- `backend/internal/app/testdata/api-samples/playlists/move-up.json`
- `backend/internal/app/testdata/api-samples/playlists/queue-empty.json`
- `backend/internal/app/testdata/api-samples/playlists/queue-missing.json`
- `backend/internal/app/testdata/api-samples/playlists/queue.json`
- `backend/internal/app/testdata/api-samples/playlists/remove-item.json`
- `backend/internal/app/testdata/api-samples/playlists/rename-noop.json`
- `backend/internal/app/testdata/api-samples/playlists/rename.json`
- `backend/internal/app/testdata/api-samples/playlists/restore-after-previous.json`
- `backend/internal/app/testdata/api-samples/playlists/restore-already-present.json`
- `backend/internal/app/testdata/api-samples/playlists/restore-at-end.json`
- `backend/internal/app/testdata/api-samples/playlists/restore-item.json`
- `backend/internal/app/testdata/api-samples/playlists/restore-original-index.json`
- `backend/internal/app/testdata/api-samples/playlists/restore-over-limit.json`
- `backend/internal/app/testdata/personal-realistic-scale.sql`
- `backend/internal/catalog/internal/releasesql/release.go`
- `backend/internal/catalog/internal/releasesql/summary_batch.go`
- `backend/internal/catalog/personal_reads.go`
- `backend/internal/catalog/personal_reads_explain_test.go`
- `backend/internal/catalog/personal_reads_integration_test.go`
- `backend/internal/catalog/personal_reads_test.go`
- `backend/internal/catalog/track_collection_stats.go`
- `backend/internal/catalog/track_collection_stats_explain_integration_test.go`
- `backend/internal/catalog/track_collection_stats_integration_test.go`
- `backend/internal/catalog/track_collection_stats_test.go`
- `backend/internal/catalog/track_summary.go`
- `backend/internal/catalog/track_summary_chunk_parity_integration_test.go`
- `backend/internal/personal/favorite_lookup.go`
- `backend/internal/personal/favorite_queue.go`
- `backend/internal/personal/favorite_queue_integration_test.go`
- `backend/internal/personal/favorite_reads.go`
- `backend/internal/personal/favorites.go`
- `backend/internal/personal/favorites_integration_test.go`
- `backend/internal/personal/fixture_integration_test.go`
- `backend/internal/personal/httpapi/favorites.go`
- `backend/internal/personal/httpapi/handlers.go`
- `backend/internal/personal/httpapi/input.go`
- `backend/internal/personal/httpapi/input_test.go`
- `backend/internal/personal/httpapi/playlists.go`
- `backend/internal/personal/order_explain_integration_test.go`
- `backend/internal/personal/performance_integration_test.go`
- `backend/internal/personal/playlist_concurrency_integration_test.go`
- `backend/internal/personal/playlist_contracts_integration_test.go`
- `backend/internal/personal/playlist_order.go`
- `backend/internal/personal/playlist_queue.go`
- `backend/internal/personal/playlist_reads.go`
- `backend/internal/personal/playlist_reads_integration_test.go`
- `backend/internal/personal/playlist_retry.go`
- `backend/internal/personal/playlist_statistics.go`
- `backend/internal/personal/playlist_statistics_test.go`
- `backend/internal/personal/playlist_types.go`
- `backend/internal/personal/playlist_undo.go`
- `backend/internal/personal/playlist_validation_test.go`
- `backend/internal/personal/playlists.go`
- `backend/internal/personal/playlists_integration_test.go`
- `backend/internal/personal/projection.go`
- `backend/internal/personal/projection_test.go`
- `backend/internal/personal/read_tx_integration_test.go`
- `backend/internal/personal/read_tx_test.go`
- `backend/internal/personal/service.go`
- `backend/internal/platform/httpserver/middleware.go`
- `backend/internal/platform/httpserver/middleware_test.go`
- `backend/migrations/0021_favorites.sql`
- `backend/migrations/0022_playlists.sql`
