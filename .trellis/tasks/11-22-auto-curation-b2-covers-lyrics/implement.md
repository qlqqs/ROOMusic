# 自动整理 B2：执行计划

分支：`feat/auto-curation-b2-covers-lyrics`（从 `main` 的 `bf1527c8` 之后拉出）。后端实现与测试交给 Codex 子代理，实现与测试由不同的子代理执行；
前端由主代理实现。子代理不提交 Git。

## 一、工作线

两条实现线并行，各在自己的工作目录里（主工作目录做“存储线”，`temp/wt-b2-sources` 的独立工作目录做“外部源线”），验收后由主代理合到分支上。

| 步骤 | 内容 | 执行者 | 模型 |
| --- | --- | --- | --- |
| S1 | 外部源线的实现 | Codex | `gpt-6.2-astra` |
| D1 | 存储线的实现 | Codex | `gpt-6.2-astra` |
| M | 主代理审阅两条线、合并两处共用文件、编译与现有测试 | 主代理 | — |
| S2 | 外部源线的测试 | Codex（另一个） | `gpt-6.2-sol` |
| D2 | 存储线的测试与接口样本 | Codex（另一个） | `gpt-6.2-sol` |
| F | 前端：歌词来源、操作记录摘要、接口合同登记 | 主代理 | — |
| V | 验收对照、变异检查复核、合同文档、提交与合并 | 主代理 | — |

### S1 外部源线（允许修改的范围）

- `backend/internal/sources/`（能力接口与类型、`Requester` 的重定向与 `GetBytes`）
- `backend/internal/sources/coverartarchive/`、`backend/internal/sources/lrclib/`（新）
- `backend/internal/steward/web/`（封面清单改走适配器；现有测试只允许改注入方式，不改断言）
- `backend/internal/curation/sources.go`、`source_finders.go`（两种新能力的注册与闸门）
- `backend/internal/app/app.go`、`steward.go`：只加两个适配器的构造、缓存清理、源的注册、管家的注入
- `backend/internal/app/dependency_rules_test.go`：只加 `sources/coverartarchive`、`sources/lrclib` 两条规则与 `steward/web`、`app` 的新增导入
- `backend/internal/app/curation_sources_integration_test.go:194` 附近对源的个数的断言（一个变三个）：这是注册三个源的必然结果，允许改这一处

管家现有的两个封面用例（`c8_test.go`、`c8_round2_test.go`）：允许把入参 `"synthetic"` 换成合成的 UUID、把注入改成
`WithCovers(coverartarchive.New(Options{BaseURL, NewClient: s.newClient, ManifestLimiter: 零间隔}))`；断言不变。

不碰：迁移、`catalog`、`operations`、`curation/writer.go`、`settings.go` 及其余 `curation` 文件。

### D1 存储线（允许修改的范围）

- `backend/migrations/0061_external_artwork_lyrics.sql` 与 `.down.sql`；`backend/internal/platform/postgres/migrations_rollback_integration_test.go` 的版本 61 夹具
- `backend/internal/operations/`（类型常量、`Outcome.AfterCommit`）
- `backend/internal/catalog/artwork/`（存储键的锁、`Describe`、`Write`、`Exists`、`Discard`；扫描路径的绑定与清理取同一把锁；`Lookup` 与引用计数看两张表）
- `backend/internal/catalog/metadata/lyrics.go`（新）；`backend/internal/library/parser/lyrics.go` 及引用 `NormalizeLyrics`、`MaxLyricsBytes` 的调用点与测试（改成新的位置）
- `backend/internal/catalog/internal/releasesql/`、`backend/internal/catalog/lyrics.go`（读取优先顺序）
- `backend/internal/catalog/governance/auto_values.go`、`revert.go`（只加设计第八节列出的导出入口）
- `backend/internal/curation/`：新文件 `external_artwork.go`、`external_lyrics.go`（写入、撤销、展示），`settings.go` 里的 `NewWriter` 与 `Writer`，`suppressions.go`（按外部身份的标记）
- `backend/internal/app/app.go`：只改 `NewWriter` 的构造与 `operationKinds`；`dependency_rules_test.go`：只改 `curation` 一条规则（`app` 那一条不动）
- 因 `NewWriter` 签名变化而必须跟着改的现有测试装配（三个调用点，只改构造，不改断言）

不碰：`sources` 及其子包、`steward`、`curation/sources.go`、`source_finders.go`。

### 共用文件的合并

`app.go` 与 `dependency_rules_test.go` 两条线都会改。两条线各自只动上面写明的那几处（依赖规则里 `app` 那一条只有外部源线改）；主代理在步骤 M 用补丁三方合并，冲突手工解决。

## 二、验证命令（范围上限，按实际改动收窄）

集成测试脚本有全局锁，两条线不能同时跑：实现阶段两条线只跑编译、`go vet` 与不需要数据库的单元测试；需要数据库的验证在步骤 M 之后串行进行。

```bash
# 编译与静态检查（两条线都跑）
cd backend && mise exec -- go build ./... && mise exec -- go vet ./internal/sources/... ./internal/curation/... ./internal/catalog/... ./internal/operations/... ./internal/steward/web/... ./internal/app/...

# 外部源线的单元测试
cd backend && mise exec -- go test ./internal/sources/... ./internal/steward/web/...

# 存储线的单元测试
cd backend && mise exec -- go test ./internal/catalog/artwork/... ./internal/catalog/metadata/... ./internal/library/parser/... ./internal/catalog/ -run 'Lyrics'

# 集成（步骤 M 之后，串行）
sg docker -c 'ROOMUSIC_TEST_RUN="TestPostgreSQLMigrationRollbackRoundTrip|TestPackageDependencyDirections" mise exec -- ./scripts/test-integration.sh'
sg docker -c 'ROOMUSIC_TEST_RUN="TestPostgreSQLCuration" mise exec -- ./scripts/test-integration.sh'          # 现有自动整理用例不回归 + 新用例
sg docker -c 'ROOMUSIC_TEST_RUN="TestPostgreSQLArtwork|TestPostgreSQLTrackLyrics|TestPostgreSQLExternal" mise exec -- ./scripts/test-integration.sh'
sg docker -c 'ROOMUSIC_TEST_RUN="TestPostgreSQLAPIResponseSamples" mise exec -- ./scripts/test-integration.sh' # 样本；首次更新时加 ROOMUSIC_UPDATE_API_SAMPLES=1
sg docker -c 'ROOMUSIC_TEST_RUN="TestPostgreSQLC8" mise exec -- ./scripts/test-integration.sh'                 # 管家联网的集成用例

# 目录读取的门禁（摘要多了一个左连接）
sg docker -c 'ROOMUSIC_CATALOG_EXPLAIN=1 ROOMUSIC_CATALOG_EXPLAIN_OUTPUT=temp/b2-explain ROOMUSIC_CATALOG_HTTP_PERF_OUTPUT=temp/b2-perf ROOMUSIC_TEST_RUN="Explain|TestPostgreSQLCatalogHTTPPerformance" mise exec -- ./scripts/test-integration.sh'

# 前端
cd frontend && mise exec -- npx vitest run src/test/api-contract.test.ts src/features/governance src/features/player/api && mise exec -- npx eslint src/features/governance src/features/player src/test
```

门禁用例的确切名字与启用参数（`ROOMUSIC_CATALOG_EXPLAIN`、`…_EXPLAIN_DIR`、`…_EXPLAIN_OUTPUT`、`…_HTTP_PERF_OUTPUT` 各用例不同）以测试文件与 `.trellis/spec/backend/catalog-rest-verification.md` 为准，由测试子代理核对后运行；跳过不算通过。

## 三、测试清单（S2、D2 的起点，用例名可调整）

外部源线（`backend/internal/sources/...`、`steward/web`，受控的本地服务与注入的时钟）：

- `Requester`：跟随重定向的放行与拒绝（主机、协议、次数、带凭据的地址）、被拒绝后不重试、每跳的请求头、一次调用只占一个许可、`GetBytes` 的 404、超限、重试、不写响应缓存、有数据目录时的失败记忆。
- Cover Art Archive：清单解析（`id` 两种类型、`release` 字段、缺 `thumbnails` 键）、release 与 release group 两种目标、404 为空、清单缓存；
  选图的三种情况；清单没有 `release` 字段时两种目标的结果；四档回退；PDF；清单用 MusicBrainz 的限速器、图片用自己的限速器（两个注入的限速器各自计数）；
  默认装配下清单的限速器与 `musicbrainz.New(Options{}).Limiter()` 是同一个实例。
- LRCLIB：两种请求的参数形式、字段映射、`null` 歌词、纯音乐、404、429 带 `Retry-After`、标题或艺人为空。
- 管家：现有 `TestC8CoverArtRedirectOfficialOnly`、`TestC8FixedSourceClientsPublicOnly` 通过；补上合同要求而缺的三项（清单 404 得空列表、四个字段的映射、请求头不含用户与实例信息）。
- 闸门：两种新能力在总开关关闭、源关闭时拒绝且零出站（并入现有 `TestPostgreSQLCurationSourcesGate` 或新增用例）；源开关接口列出三个源。

存储线（隔离的 PostgreSQL）：

- `TestPostgreSQLExternalArtworkPriority`：只有外部、本地出现、本地被全量扫描删除三种状态下，发行摘要、专辑摘要、曲目摘要与图片接口的结果。
- `TestPostgreSQLExternalArtworkRescan`：全量重扫、改名移动、认领之后行与文件都在；扫描的孤儿清理不删被外部引用的文件。
- `TestPostgreSQLExternalArtworkApply`：记录的各项；三种跳过原因（不再提出分内容与外部身份两种）；总开关关闭与开启者失效；无效图片、跳过与同键重放都不落盘；
  事务回滚后新写的文件被删除、原有的文件不被删除；旧行的文件丢失时按新写入处理。
- `TestPostgreSQLExternalArtworkRevert`：单条、按批、按来源；撤销后的文件清理与共享文件不删（V11）；不再提出；人工修改版本号不变；归属不符时 `revert_conflict`。
- `TestPostgreSQLExternalLyricsPriority`、`…Apply`、`…Revert`：歌词的对应用例；`source` 为 `online`；本地歌词解析不出可显示的行时用外部的。
- `TestPostgreSQLExternalConcurrent`：同一发行两次封面写入、写入与撤销、两个发行同一内容一写一撤（用事务锁制造交叠，不靠时间）；结束时行与文件一致。
- 操作记录列表：两类改动的 `changes` 与 `revertible`；撤销记录的展示；读不懂的状态不让列表失败；管家 `revert_operation` 对两类操作的预览与执行。
- 现有扫描的封面用例不回归（绑定与清理改成取键锁）。
- 迁移回退往返（版本 61）；包依赖方向。
- 接口样本：新增 `tracks/lyrics-online`、`operations/external-list`、`operations/external-reverted`；更新 `curation/sources`、`curation/source-updated`（如受影响）；其余样本逐项不变。
- 变异检查（每条线至少各 4 个，记录变异内容与被哪个用例发现）：重定向主机判断、选图顺序、读取的 `COALESCE` 顺序、`local_present` 与 `occupied` 的判断、
  撤销的归属判断、引用计数只看一张表、`AfterCommit` 在失败时也调用。

## 四、风险点与回退

- `releasesql` 的连接片段被十几处读取共用：改完先跑现有的目录与样本用例确认没有外部封面时逐项不变，再加新用例。
- `NewWriter` 签名变化牵动现有测试的装配：只改构造。
- 迁移 0061 合入后不可修改；合入前回退往返测试必须通过。
- 任一条线验收不过：该线返工，不影响另一条；两条线都没合到分支之前，`main` 不受影响。

## 五、启动前检查

- [x] `prd.md`、`design.md`、`implement.md` 齐备；`implement.jsonl`、`check.jsonl` 已登记规范与调研文档。
- [x] 规划评审完成并记录（见下）。
- [x] 用户的长期授权覆盖“规划完直接实现”（2026-11-22）。

## 规划评审

评审者：规划评审子代理（Plan，2026-11-22，对照真实代码的静态评审，没有运行测试）。结论：两条线并行可行；6 处必须修改，全部采纳；13 条建议采纳 12 条。

必须修改（全部采纳，已写进 `design.md`）：

| 编号 | 问题 | 改法 |
| --- | --- | --- |
| M1 | 封面文件“数引用再删”与“落盘再写行”没有互斥：两个发行拿到同一张图时，撤销一个会删掉另一个刚要引用的文件；撤销后的同键重放会留下孤儿文件 | 按存储键的事务级锁；事务外只校验不落盘，落盘挪进执行器；`Discard` 与扫描的绑定、清理取同一把锁（第八、十节） |
| M2 | 管家两个现有封面用例在原设计下通不过：入参不是 UUID、夹具没有 `release` 字段、默认实例拿不到测试注入的客户端、全局限速器让用例变慢 | 清单的结构校验只要求 `images` 是数组；`ReleaseID` 可为空；默认实例的客户端工厂用闭包；测试注入零间隔限速器、入参换成合成 UUID（第三、五节） |
| M3 | 存储线的允许范围漏了三处：`NewWriter` 在 `settings.go`；`app` 的依赖规则没有 `catalog/artwork`；两个新 `Kind` 需要的发行展示信息与加锁入口是 `governance` 的私有项 | `NewWriter(db, 数据目录, logger)` 自己构造存储；`governance` 导出所需入口；范围补上 `settings.go`、`revert.go`（第八节与本文第一节） |
| M4 | 回退脚本会被 `steward_plan_items.operation_id` 的外键挡住；不先落实延迟约束改不了类型约束；夹具不能改回退不恢复的表 | 回退脚本四步的顺序与夹具的限制（第六节） |
| M5 | 不再提出只按图片字节判定，同一张图换一档缩略图就绕过了 | 撤销时再写一条按外部身份的标记，写入时两条都查（第六、八、九节） |
| M6 | 图片的请求器不给缓存目录时失败记忆静默失效 | 图片的请求器照样传数据目录，`GetBytes` 不写响应缓存但使用失败记忆（第二、三节） |

建议修改：

| 建议 | 处理 |
| --- | --- |
| 两个 `Kind` 实现撤销预览；`Describe` 对读不懂的状态不报错 | 采纳（第九节） |
| 歌词写入先取目录写锁再解析曲目所在的发行 | 采纳（`LockAutoReleaseOfTrack`）；按新发行筛选看不到旧记录写进合同 |
| 图片第一跳也是 MetaBrainz 的网关，P6 的理由不准确 | 采纳：改写理由，明确接受对网关合计每秒 2 次（文档写明 Cover Art Archive 没有限速规则） |
| `Requester` 的三个实现注意点（复制客户端、拒绝时返回 `egress.Error`、取字节的时限） | 采纳（第二节；`GetBytes` 时限 60 秒） |
| 断言默认装配下清单与 MusicBrainz 共用同一个限速器 | 采纳（测试清单） |
| 点名要改的现有断言（源的个数） | 采纳（本文第一节） |
| 恢复备份后外部封面的文件丢失没有修复路径 | 采纳，做法不同：旧行的文件不在时，新的写入视同没有外部封面并替换旧行；“发现文件丢失并重新获取”由子任务 C 的缺口查询负责（已记入父任务计划） |
| 来源标识按不带冒号的模式校验 | 采纳（第八节） |
| 歌词“本地已有”的判断与读取统一 | 采纳（`ParseLyrics` 成功） |
| 按发行筛选标记看不到歌词的标记 | 采纳为合同里的已知限制，留给 E1 |
| 前端遗漏（`OPERATION_TYPES`、测试替身、撤销后歌词查询失效、歌词合同） | 采纳（第十二节） |
| 删掉没有调用方的设计（两个读函数、`Approved`、解析器里的转发、常量命名） | 采纳 |
| 文字与门禁命令的环境变量 | 采纳 |
| 给 C 一个下载前可查的“是否已标记”读函数 | 不在本任务做：形状由 C 的匹配循环决定，已记入父任务计划 |

选定结果：按修订后的规划实施。实现授权来自用户 2026-11-22 的长期指示。
