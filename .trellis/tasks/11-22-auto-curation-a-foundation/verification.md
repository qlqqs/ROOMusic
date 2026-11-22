# 自动整理 A：验收记录

日期：2026-11-22。分支 `feat/auto-curation-a-foundation`。全部在隔离的 PostgreSQL（`./scripts/test-integration.sh` 建的独占 Compose 项目）上用合成数据运行。
“测试代理”指写测试的 Codex 子代理，“主代理”指做验收的一方；每条写明是谁在哪一轮运行的。

## 逐条结果

| 验收 | 用例 | 结果 | 运行 |
| --- | --- | --- | --- |
| A-V1 | `TestPostgreSQLCurationFieldPriority` | 通过：八个字段逐个核对 人工 → 自动 → 扫描，六个可空字段在两层各设空 | 测试代理；主代理的全集运行再次通过 |
| A-V2 | `TestPostgreSQLAPIResponseSamples`、`TestPostgreSQLArtistAPIResponseSamples`、四个目录 EXPLAIN、`TestPostgreSQLCatalogHTTPPerformance` | 通过：只有返回修正状态的五份样本各多八个 `"auto": null`，其余 774 份逐字不变；10 万曲目下最近新增 p95 59–64 ms（门槛 150）、`added_at_desc` 首页 104–114 ms（门槛 300） | 测试代理（EXPLAIN 与性能要设启用参数，全集运行里是跳过的） |
| A-V3 | `TestPostgreSQLCurationRescan` | 通过：原样重扫与改标签后重扫，自动值、写入者、`auto_revision` 与自动行的 `xmin` 不变 | 测试代理；全集再次通过 |
| A-V4、A-V5 | `TestPostgreSQLCurationIndependentRevisions` | 通过 | 同上 |
| A-V6 | `TestPostgreSQLCurationRevertOrder` | 通过：过早撤销返回改写者的操作 ID，倒序撤销恢复前任写入者 | 同上 |
| A-V7 | `TestPostgreSQLCurationArtistProjection` | 通过：署名、艺人页专辑、按名排序与搜索投影随写入与撤销变化 | 同上 |
| A-V8、A-V9 | `TestPostgreSQLCurationBatchRevert` | 通过：批次起止、筛选、详情计数、全撤、部分跳过；同键重试返回 0 条、撤销记录数不变 | 测试代理（返工后）；全集再次通过 |
| A-V10 | `TestPostgreSQLCurationProviderCursor` | 通过：101 条按 100＋1 续做，另一来源的值与写入者保留 | 同上 |
| A-V11 | `TestPostgreSQLCurationSuppression` | 通过 | 测试代理；全集再次通过 |
| A-V12 | `TestPostgreSQLCurationEnabler` | 通过：默认关、关闭后保留已有值、记在开启者名下、开启者停用与降级后拒绝 | 同上 |
| A-V13 | `TestPostgreSQLCurationMemberDenied` | 通过：九个请求均 403 | 测试代理（返工后）；全集再次通过 |
| A-V14 | `TestPostgreSQLCurationAttention` | 通过：关注数 1 → 0 → 1 | 测试代理；全集再次通过 |
| A-V15 | `TestPostgreSQLMigrationRollbackRoundTrip/0058_auto_curation.sql` | 通过：0057 → 0058 → 0057 → 0058，回退后与 0057 的快照一致 | 实现代理、测试代理；全集再次通过 |
| A-V16 | 前端 Vitest：`features/governance`、`features/artists`、`app/operations`、`test/api-contract`、`app/release-corrections` 等受修正状态影响的用例；`tsc --noEmit`；`eslint` | 通过 | 主代理 |
| A-V17 | `TestPackageDependencyDirections`、`TestPostgreSQLCurationConcurrent`（两次自动写入、撤销与自动写入）、五次变异检查 | 通过；五次变异均被目标断言检出并已逐字节还原 | 测试代理；依赖规则与并发用例在全集里再次通过 |

查询计划（`TestPostgreSQLCurationFilterPlans`，测试代理，最后一轮）：操作记录按来源、按批次筛选命中 `operations_provider_idx`、`operations_batch_idx`；
范围撤销的候选查询两种范围都按索引顺序返回，整份计划没有排序与顺序扫描，首段与续段各约 0.1 ms（5,000 条操作、目标 200 条、每段 100 条）。

## 变异检查（A-V17）

| 临时变异 | 检出的断言 |
| --- | --- |
| `catalog/governance/auto_values.go` 跳过目录写锁 | 两个并发子用例报告“请求越过事务持有的目录锁” |
| `curation/settings.go` 跳过关闭开关的判断 | `CurationEnabler`：关闭后的原因不是“开关已关闭” |
| `curation/settings.go` 跳过开启者有效性判断 | `CurationEnabler`：开启者停用后写入成功 |
| `identity/guard.go` 跳过管理员角色判断 | `CurationMemberDenied`：九个请求均未返回 403 |
| `0058_auto_curation.down.sql` 删掉自动署名的清理 | 迁移往返在回到 0057 时失败 |

## 主代理的全集运行

改动涉及所有目录读取共用的视图、艺人署名的层选择与共同写入流程，影响面无法局部覆盖，所以在合并前跑了一次 `./scripts/test-integration.sh` 的全集：
833 个通过、5 个跳过（要启用参数的 EXPLAIN 与性能用例）、4 个失败。四个失败都是并行满载下的超时，没有断言失败：

- `TestPostgreSQLMigrationPreBackupBeforeDDLAndLockProbe`、`…EmptyDatabaseDoesNotBackup`、`…OpenReturnsClassifiedError`：“清理独立测试数据库：timeout”。
  主代理单独重跑 `^TestPostgreSQLMigrationPreBackup`，四个用例全部通过（每个不到 1 秒）。
- `TestPostgreSQLAPIResponseSamples`：“目录后台退出：context deadline exceeded”（满载下 160 秒）。沿用测试代理在最后一次实现改动之后的单独运行结果（通过，60 秒）；
  那之后只改过 `operations` 包的一个测试文件。

## 实现期间的修订

- 修正成功的响应与读取同形，也带 `auto`（前端把它当新状态用）；因此两份录有修正响应的搜索样本同样多出 `auto`。A-V2 的措辞已修订。
- 范围撤销的重试语义简化：已撤销的操作不再是候选，`reverted` 只统计这次实际撤销的条数（design 第四节已修订）。
- 操作记录列表项的 `batch_id` 在没有批次时省略，既有样本因此不变。
- 艺人别名的来源在前端按字符串解码、不按取值显示标签，`auto` 这个新取值不需要前端改动。

## 没有验证的

- 真实设备与真实浏览器：本任务没有界面变化，没有做。
- 开关打开后的真实使用：本任务没有自动写入的调用方，只有测试直接调用 `curation.Writer.Apply`。
- 回退 0058 之后的派生数据：回退脚本本身有往返用例；“回退之后要做一次完整重扫才会刷新有效署名与搜索值”来自读代码的结论，没有用例。
