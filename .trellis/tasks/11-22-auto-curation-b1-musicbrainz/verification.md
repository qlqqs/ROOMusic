# 自动整理 B1：验收记录

日期：2026-11-22。分支 `feat/auto-curation-b1-musicbrainz`。全部用合成数据与本地的受控服务，没有请求真实的 MusicBrainz。

执行者：P0（迁移）、S1/S2（外部源一线实现）、T1（扫描标识一线实现）、S3 与 T2（两条线的测试）是五个不同的 Codex 子代理；
实现审阅、合并、前端登记、合同文档与下表里标注“主代理”的运行由主代理完成。子代理的完整报告在 `temp/`
（`b1-s1-impl-report.md`、`b1-t1-impl-report.md`、`b1-sources-test-report.md`、`b1-scan-test-report.md`），不进仓库。

## 逐条结果

| 编号 | 结果 | 依据（谁在哪一轮运行） |
| --- | --- | --- |
| B1-V1 | 通过 | `platform/egress` 与管家各包的单元测试：S1 自检与 S3 运行；出站链路的现有集成用例 `TestPostgreSQLC8TavilyQuotaHTTPChain`：主代理合并后运行 |
| B1-V2 | 通过 | 管家工具级现有用例（S3 逐文件核对：只改了注入方式，断言保留）；新增 `TestMusicToolsProjectOnlyPublicFields` 逐字节比较两个工具的输出；限速器排队超时的白盒断言搬到 `sources/limiter_test.go` |
| B1-V3 | 通过 | `TestSharedOutboundInterval`、`TestLimiterPriority`、`TestLimiterWaitDeadlineAndCancellation`（S3，注入时钟） |
| B1-V4 | 通过 | `TestCacheLifetime`、`TestFailureMemory` 及目录为空、目录不可写、结构不对的 200 的用例（S3） |
| B1-V5 | 通过 | `TestRetrySchedule`、`TestRetryNetwork`、`TestFailureMemory`（S3） |
| B1-V6 | 通过 | `TestLookupProjections`、`TestSearchQueries`（S3，对本地服务断言请求与解析） |
| B1-V7 | 通过 | `TestDiscTOC`：调研核实的例子得到 `bDCCdX8vZbnGiGD4tcN8aGNdkZc-`（S3） |
| B1-V8 | 通过 | `TestPostgreSQLCurationSourcesGate`、`TestPostgreSQLCurationSourcesGateDuringWait`（S3，集成） |
| B1-V9 | 通过 | `TestPostgreSQLCurationSourcesSettings`（S3；主代理合并后又运行一次） |
| B1-V10 | 通过 | `TestParseIdentifiersByFormat` 等解析器用例，12 种格式或标签版本 × 10 个种类（T2） |
| B1-V11 | 通过 | `TestPostgreSQLTrackIdentifiersReplaceAndClear`、`…CueVirtual`、`TestBuildSourceObservationsWithIdentifiers`、`TestCandidateMetadataSignatureIdentifiers`（T2；第一条主代理合并后又运行一次） |
| B1-V12 | 通过 | `TestPostgreSQLTrackIdentifiersCommonVersionUpgrade`；`TestPostgreSQLAPIResponseSamples` 不带更新参数逐项不变（T2 在扫描标识的工作目录里运行，S3 在主工作目录里运行；合并后没有重跑，见“未重跑的项”） |
| B1-V13 | 通过 | `TestParseIdentifierReleaseFieldsAndLegacyISRC`、`TestPostgreSQLTrackIdentifiersReleaseFields`（T2） |
| B1-V14 | 通过 | `TestPostgreSQLCurationSourcesLinks`（S3） |
| B1-V15 | 通过 | `TestPostgreSQLMigrationRollbackRoundTrip`：P0 运行；主代理在两条线合并后再运行一次（0059 与 0060 一起） |
| B1-V16 | 通过 | `TestPackageDependencyDirections`（S1、S3、主代理合并后）；`TestSourceLogPrivacy`（S3） |
| B1-V17 | 通过 | `frontend/src/test/api-contract.test.ts`：主代理登记两份新样本后运行，683 通过、1 跳过；该文件的 eslint 通过 |
| B1-V18 | 通过 | 新增 `sources-contracts.md`；更新 `curation-contracts.md`、`steward-web-contracts.md`、`steward-runtime-contracts.md`、`library-scan-tags.md`、`library-scan-incremental.md`、`database-guidelines.md`、`directory-structure.md`、`index.md`；ADR 0006 补充四处说明。路线图与 ADR 第 13 节的提交号在合并后补 |

## 变异检查

S3（外部源一线，8 项全部被用例抓到）：闸门去掉总开关条件、去掉源开关条件、去掉开启者条件；去掉出站前的放行回调；把断连当成 404；
后台不让对话优先；disc ID 不补零；把用尽重试的 503 写进文件缓存。

T2（扫描标识一线，5 项全部被用例抓到）：“Track Id” 映射成曲目 ID；虚拟轨带上录音 ID；非法的 MBID 不丢弃；`TXXX:ISRC` 写进原字段；落库不先删旧行。

每项都在运行后还原；两个测试子代理结束时都核对了非测试文件与开始时一致。

## 主代理实际运行的命令

- `cd backend && mise exec -- go build ./...`、`gofmt -l internal/`（无输出）、
  `go vet ./internal/library/... ./internal/catalog/... ./internal/sources/... ./internal/curation/... ./internal/steward/web/... ./internal/app/...`
- `go test ./internal/catalog/metadata/... ./internal/library/parser/... ./internal/sources/...`
- 合并后的集成检查：`ROOMUSIC_TEST_RUN="^TestPostgreSQLMigrationRollbackRoundTrip$|^TestPackageDependencyDirections$|^TestPostgreSQLTrackIdentifiersReplaceAndClear$|^TestPostgreSQLCurationSourcesSettings$"`，退出 0。
- `ROOMUSIC_TEST_RUN="^TestPostgreSQLC8TavilyQuotaHTTPChain$"`，退出 0。
- `cd frontend && npx vitest run src/test/api-contract.test.ts`、`npx eslint src/test/api-contract.test.ts`。

## 未重跑的项与理由

- 没有跑集成测试全集：本任务不改视图与现有接口，两条线改动的包不重叠。
- 合并之后没有重跑 `TestPostgreSQLAPIResponseSamples` 与两条线各自的其余新用例：它们在合并前各自的工作目录里通过，
  合并只是把互不相交的两组文件放到一起；合并后跑了构建、依赖规则、迁移往返与每条线各一条集成用例作为抽查。

## 实现中的修订

| 修订 | 原因 |
| --- | --- |
| 失败的进程内记忆只属于后台，对话每次都真实尝试 | 原设计让对话也受它影响；MusicBrainz 的 503 很常见且转瞬即逝，对话里的重试不应在十分钟内必然失败 |
| 闸门的核对用普通事务、原样复用 `loadSettings` | 原设计写的是只读事务，实现里因此出现了第二份“启用状态的管理员”判断；改回单一定义 |
| disc ID 在校验前去掉首尾空白与 NUL | Vorbis 与 APEv2 的值在收集标识时还没有经过截断 |
| `ordinal` 是同一种类内的出现次序，从 0 起 | 设计没有规定起点 |
| 规划审阅采纳的 11 条修改 | 见 `prd.md` 的审阅记录 |

模型：`implement.md` 里计划用 `gpt-6.2-sol` 的两步里，T2 与 P0 用的是它；其间网关一度对两个模型都返回“不受支持”，恢复后继续，没有换用其他模型。

## 没有验证的项

- 条码的 `barcode:A OR barcode:B` 写法与 `/ws/2/isrc/` 的返回结构没有对真实服务核实（调研的验证请求遇到 503，按次数上限没有重发）；
  实现按官方文档与服务端源码写，由父任务收尾的真实试用覆盖。
- 上千首曲目的大型套装在单次读取时是否会超过 8 MiB 或被网关截断，没有实测。
- 闸门没有生产调用方：它的行为只由测试里的调用验证，真实的后台匹配在子任务 C。
- 升级后第一次扫描会重新解析全部文件的标签：只在合成曲库上验证了正确性（ID 不变、标识落库），没有在大曲库上测耗时。
- 没有界面改动，所以没有真机核对。
