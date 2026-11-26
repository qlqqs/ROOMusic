# 本地辅助实施顺序

本计划的命令从仓库根目录执行。实现代理修改产品、脚本和既有调用点，新增测试留给测试代理。下列命令对应各步，实施结果记录在文末。每次集成测试由仓库脚本创建独立临时库和 Compose 项目。

## 1. 迁移与持久化约束

- 按合入时最高版本取下一号，在 `backend/migrations/` 增加同号的 `NNNN_curation_local_assist.sql` 与 `.down.sql`。加入 `local_model_groupings`、`local_credit_parts` 及操作类型，字段和所有者见设计 §4。旧 auto value 无拆分段时按原方式重建署名。安排旧 B1 在途项退休，保留操作和子任务 7 的用量账。
- 核对字段操作写业务行、后写 operations 的时序。外键应允许当前事务内延迟核对。down 恢复旧类型集合，仅处理本次新增的数据，恢复普通扫描及署名派生的步骤见设计 §5。
- 验证迁移文件成对和历史连续性，再用合成库核对迁移执行。

```sh
(cd backend && mise exec -- go test ./internal/platform/postgres -run '^TestEmbeddedMigrationRollbackFiles$')
ROOMUSIC_TEST_RUN='^TestPostgreSQLMigrationFreshAndRerunMetadata$' mise exec -- ./scripts/test-integration.sh
```

## 2. 从扫描事实投影一个完整目录

- 修改 `backend/internal/catalog/{curation_evidence,curation_legacy}.go`，增加 `curation_local_evidence.go`。按目录读取成员、文件和 CUE 引用、原标签与来源、注释、按编码列出的各种读法、未选值和平票。给每一条可拆署名稳定引用。抽出 `backend/internal/catalog/metadata/legacy_encoding.go` 的给定编码解码与多段署名组合，让 `backend/internal/catalog/ingest/legacy_encoding.go` 复用。沿用 GBK、Big5、Shift-JIS 的无损判定。
- 在 `backend/internal/curation/{local_items,model_targets}.go` 及 `program/queue_projection.go` 接设计 §1 的触发条件、目录幂等投影与全成员互斥。用 `same_dir_split` 与多个扫描专辑名键识别 S4 结果。未标旧编码的历史行只根据实际保留的原字节恢复读法。显式空交集保留无解。局部刷新和跨页刷新必须读取同一份目录成员。未知碟目录和普通单碟入口按设计末节的已定口径。
- 风险是把本轮生成的自动值读回为原文、用显示容器号当碟号、因根内同名文件或分页漏读某张。核对可观察证据和真实扫描的解码规则。

```sh
(cd backend && mise exec -- go test ./internal/catalog/metadata -run '^TestLegacyDecodings')
(cd backend && mise exec -- go test ./internal/catalog/ingest -run '^TestOrganize(MixedDirectoryFallback|FieldVotesRetainTiedDecisions|TrackNumbersSeparateFromSort)$')
ROOMUSIC_TEST_RUN='^TestPostgreSQLScanReadAlbumEncoding$' mise exec -- ./scripts/test-integration.sh
```

## 3. 工具合同、核对与模拟回包一起切换

- 更新 `backend/internal/curation/local_submit.go`、`stewardtools/patrol.go` 的本地部分、`model/item.go` 及 `backend/internal/app/{curation_patrol_prompt,curation_model}.go`。本地准备只读本项证据，允许工具仅有 `submit_local_cleanup`，新旧估算与发送入口都使用同一份新 schema。四类回包的核对通过后再固定写入计划。区分明确保留、完全无效、部分可用和过期。局部标点比较放本地核对函数。
- 修改 `backend/internal/app/testdata/curation_regression/fixed-script.json` 中 B1 的规则和 `backend/internal/upstreammock/script.go` 的绑定与默认回复。GBK 的选择按发行引用，文字按目标字段，拆分按原署名引用，归组按文件引用。只用本次输入绑定嵌套引用。规则首条命中，避免旧乱码短串误命中。没有动作时交明确理由。测试代理的正反样例复用这个脚本格式，固定基线继续只读。
- 同步调整 `backend/internal/app/{curation_metrics_model,curation_local_directory,curation_patrol,curation_live_corpus}_integration_test.go` 里既有工具回包和旧字段断言，保留既有合成用例。真实试用只做调用点编译。风险是旧脚本的裸 item_id 被当作有效决定，或门户允许别类的工具/外部证据进入。
- 验证当前项绑定、消息取证和已有指标驱动的新结构。

```sh
(cd backend && mise exec -- go test ./internal/upstreammock -run '^TestScriptCurrentItem$')
(cd backend && mise exec -- go test ./internal/curation/model -run '^TestCurationModelRulesFirstMessageLocalEvidence$')
(cd backend && mise exec -- go test ./internal/app -run '^TestMetricsOraclePatrolAndLocalValues$')
```

## 4. 写自动字段和可撤销署名

- 修改 `backend/internal/curation/{local_submit,writer,local_search}.go`、`backend/internal/catalog/governance/{auto_values,track_auto_values,auto_revert,track_auto_revert}.go`、`backend/internal/catalog/internal/artistgraph/facts.go`、`backend/internal/catalog/curation_artist_credits.go`。同事务按 owner 保存原显示文字及拆分段、刷新有效署名，按段生成有序关系。识别读取时保留原文和“推断”来源，不把模型段提升为已证实的外部身份。
- 接 `backend/internal/curation/program/{enrich,track_fields}.go`。认到具体一版时，原本 `vacant`、`OnlyEmpty` 都可能跳过 B1 值。在计划和执行两端定向放行当前由 B1 持有、该匹配级别允许的字段。人工与其他 owner 在事务内重查。A2.5 由 `program/artist_links.go` 在确定性拼接失败的分支提供本地目标，随 `program/pass.go` 的匹配结算入队。不引发第二次 MB 搜索。
- 风险是值相同但分段不同被判 unchanged、高层覆盖后低层撤销误删现值、重启执行第二个目标时被第一个目标的写入判为过期。按固定计划和逐目标幂等键重放。验证已有本地字段、有效搜索文字和撤销。

```sh
ROOMUSIC_TEST_RUN='^(TestPostgreSQL整理本地模型只写本地片段并保留碟号曲序|TestPostgreSQL整理无标签编号目录中的专辑名与艺人|TestPostgreSQLTrackAutoRevert)$' mise exec -- ./scripts/test-integration.sh
```

## 5. 登记归组、消费和撤销

- 新增 `backend/internal/{curation,catalog/governance}/local_grouping.go`，在 `backend/internal/app/app.go` 注册操作类型、单项描述和撤销处理。持锁复核整套成员、人工并入/拆分、旧 owner 后写一条分配。同步标记目标目录需要归组。撤销、标记和局部确认请求在同一事务中变更。
- 修改 `backend/internal/catalog/ingest/{staging,identity_plan,organizer}.go`，新增 `model_grouping.go`。修改 `backend/internal/library/{scanner,scan_scope,curation_observations,candidate_signature}.go`。先沿用普通候选，再应用同一条有效结构记录。让身份规划和保存使用同一个候选键、同一种编号。局部扫描按登记的成员目录扩展，在确认范围里重建。模型位次记录为推断，模型结构摘要参与增量跳过判定。
- 风险是单次扫描仅处理兄弟目录的一半、撤销后缓存命中、或人工操作恰在登记与消费之间。以实际覆盖、文件摘要及写锁下的人工结构复核为准。验证目录扫描、已有手动并入和原生碟规则。

```sh
(cd backend && mise exec -- go test ./internal/catalog/ingest -run '^TestOrganize(DiscParentsPreserveNamedAndBonusMedia|FourContinuityModes)$')
ROOMUSIC_TEST_RUN='^(TestPostgreSQLDirectoryScanScope|TestPostgreSQLReleaseMergeRescanKeeps)$' mise exec -- ./scripts/test-integration.sh
```

## 6. 接通程序等待和规则升级

- 修改 `backend/internal/curation/program/{worker,queue_projection,queue_query,pass,release_state}.go` 和 `backend/internal/app/app.go`。以实际模型配置和本代未结 B1 项设发行依赖，识别出站前复核。相关工作重新就绪时刷新投影。成功登记归组后的首次识别读取已归组并确认的结构。A2.5 是已匹配后的拆分补办，不把识别改回未完成。串接时保留子任务 6 合入的声纹/来源条件。
- `LocalRuleVersion` 在这里统一由 3 升为 4，并用于 `ProjectLocalItems` 的键、行和运行时校验。升级保留历史操作和结论，仅给新增或确实改变的 B1 输入投影。避免模型缺配时遗留待办永远卡住程序，也避免在额度等待时提前查。
- 验证单独升级不触发已匹配重搜，以及固定样本的局部查询顺序。

```sh
ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationLocalRuleUpgradeQuiet$' mise exec -- ./scripts/test-integration.sh
ROOMUSIC_CURATION_METRICS_SUITE=fixed ROOMUSIC_CURATION_METRICS_SAMPLES='embedded-artist,legacy-many,filename-only' ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
```

## 7. 同步合同与合入验收

- 在 `.trellis/spec/backend/{curation-contracts,steward-runtime-contracts,library-scan-grouping,governance-contracts,database-guidelines}.md` 同步本地辅助的触发、输入/工具、最低自动层、结构记录、撤销、归组消费和版本。修正旧“只交文字”“唯一解由整理写入”“辅助不改变曲序”等条款。保留子任务 9、10 的章节归属。用下列命令检查合同改动、运行包依赖门禁和固定样本回归。
- 固定套件用同一批合成媒体、真实扫描、回环模拟上游，分别给出纯程序与程序加脚本两行。交付列“认对张数、认错张数、每张请求数、识别用时”的基线、实测和差值，并附有变化的逐张明细。验收为认错 0，认对不降、请求数及用时不增。命令通过但保留了基线错项时仍记录未达标。指出样本和负责规则，保留原基线。测试代理只执行与其新增用例对应的命令，结果复用。

```sh
git diff --check
(cd backend && mise exec -- go test ./internal/app -run '^TestPackageDependencyDirections$')
ROOMUSIC_CURATION_METRICS_SUITE=fixed ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
```

## 测试代理补充清单

用合成数据为每个新行为配对照。以下为建议的新用例名，可直接作为定向运行过滤器。

| 新用例 | 断言 |
| --- | --- |
| `TestPostgreSQLLocalAssistSevenReasons` | 七类各有脚本回包，再对同样输入移除模型，比较扫描值、当前分组、缺号、零 B1 会话及程序合法请求。正反包含能由扫描确定的唯一解、连续混放、已知 CD2，以及保持一个人的 & 署名。同艺人的两张独立专辑放在相邻目录，脚本明确保留两张，验证下轮扫描不合并。目录名带艺人、真实专辑标签带艺人各跑一条。前者由程序用补全的两个名字认到指定版。 |
| `TestLocalAssistChecks` | 四类各有通过/拒绝。标题覆盖点、下划线、连字符、全半角等价、跨片段拼字和知识补字。编码覆盖多字段/多段署名、空交集、混选或提交候选外编码。拆分覆盖原连接词、空格、顺序和重复出处。结构覆盖缺文件、越界文件、重复、同碟重号及跨碟同号。单项混合有效与无效结果仍只写已核准部分。 |
| `TestPostgreSQLLocalAssistModelContract` | 同目录多发行、跨页、重复文件名及 CUE 共享父文件下证据完整。注释、读法、原路径、来源、冲突均可见。消息与工具清单没有外部候选/规范名/联网工具。超长保留完整本地证据，建会话前搁置。明确无法判断、空答三次、暂停/额度按子任务 7 结算。 |
| `TestPostgreSQLLocalAssistBeforeMatch` | mock 记录证明同发行 B1 提交/结算早于其首次 MB 请求。其他发行继续。缺艺人无编号、合辑、已有编号分别验证程序门槛。等待中移除模型、终止搁置均放行。A2.5 仅申请本地署名，已匹配不重搜。覆盖 B1 一级→MB 更高层→单条撤销，同值但级别/署名不同也正确换 owner，人工值优先。 |
| `TestPostgreSQLLocalAssistGroupingRevert` | 提交即时仅存一份计划，下一次归组才改变成员、碟号、曲号。增量重扫无文件变化也生效。撤销前后分别测试未消费和已消费、按条/批次/来源、提交后人工合并或拆分、已匹配结构、重启和文件变化。普通兄弟目录仍独立。 |
| `TestPostgreSQLLocalAssistCreditParts` | 发行/曲目、重复标签、跨语言不相同的外部署名。显示串逐字不变、有效艺人按返回原序投影。重扫、人工改值、MB 接管、撤销和再投影不恢复已撤的推断，不误建艺人外部身份。 |
| `TestPostgreSQLLocalAssistUpgrade` | 用迁移前合成库做 up/down/up。旧已决、未决、部分写入、旧额度和旧撤销分别核对，旧会话提交被拒。新规则无新增目标时不重问。撤销归组后降级可恢复普通扫描。 |
| `TestLocalAssistScriptBindings` | 发行/曲目/文件/原署名引用只绑定本次目录输入。重复使用同一脚本模板对不同 UUID 仍正确，用知识回包测试拒绝。固定脚本和评分清单独立。 |

```sh
(cd backend && mise exec -- go test ./internal/curation -run '^TestLocalAssistChecks$')
(cd backend && mise exec -- go test ./internal/upstreammock -run '^TestLocalAssistScriptBindings$')
ROOMUSIC_TEST_RUN='^TestPostgreSQLLocalAssist(SevenReasons|ModelContract|BeforeMatch|GroupingRevert|CreditParts|Upgrade)$' mise exec -- ./scripts/test-integration.sh
```


## 本轮实现交接

已实现 B1 目录项的投影、四类回包核对、一级推断与撤销、扫描消费、主模型领取和识别前等待。提供 `curation.RequestLocalCreditSplit(ctx,tx,releaseID,trackID,role,sourceRef,raw)`。子任务 5 在确定性拼接失败的结算事务中调用；已有逐标签引用原样传入，引用采用曲目 ID、标签键与出现序。若失败针对整个采用的扫描署名，传空引用，由入口以目标和角色定位并逐字复核扫描原文。生产者在确定性拼接失败分支收集本地原署名引用，随匹配结算事务投影。

本分支下一连续迁移为 `0103_curation_local_assist`，主代理合入时顺延编号。迁移增加署名段与归组表、目录成员和历史决定索引，登记新操作类型，退出现有未结旧协议项。降级恢复操作类型集合。设计 §5 记录了有历史推断数据时的回退顺序。归组保护清单 `other_paths` 保存同一目录中人工或已匹配成员的路径，供扫描区分新增文件与已有受保护文件。

实际改动文件按目录列出。

| 目录 | 文件 |
| --- | --- |
| `.trellis/spec/backend/` | `curation-contracts.md`、`database-guidelines.md`、`governance-contracts.md`、`library-scan-grouping.md`、`steward-runtime-contracts.md` |
| `.trellis/tasks/11-26-curation-local-assist/` | `design.md`、`implement.md` |
| `backend/internal/app/` | `app.go`、`curation_live_corpus_integration_test.go`、`curation_local_directory_integration_test.go`、`curation_metrics_model_integration_test.go`、`curation_patrol_integration_test.go`、`curation_patrol_prompt.go` |
| `backend/internal/app/testdata/curation_regression/` | `fixed-script.json` |
| `backend/internal/catalog/` | `curation_artist_credits.go`、`curation_local_evidence.go` |
| `backend/internal/catalog/governance/` | `auto_revert.go`、`track_auto_revert.go` |
| `backend/internal/catalog/ingest/` | `legacy_encoding.go`、`observation.go`、`organizer.go`、`staging.go` |
| `backend/internal/catalog/internal/artistgraph/` | `facts.go` |
| `backend/internal/catalog/metadata/` | `legacy_encoding.go` |
| `backend/internal/curation/` | `local_grouping.go`、`local_items.go`、`local_submit.go`、`model_queue.go`、`model_run.go`、`model_targets.go`、`pending_items.go`、`suppressions.go`、`writer.go` |
| `backend/internal/curation/model/` | `item.go` |
| `backend/internal/curation/program/` | `enrich.go`、`queue_query.go`、`queue_worker.go`、`release_route.go`、`track_fields.go`、`worker.go` |
| `backend/internal/curation/stewardtools/` | `patrol.go` |
| `backend/internal/library/` | `scan_scope.go`、`scanner.go` |
| `backend/internal/upstreammock/` | `script.go` |
| `backend/migrations/` | `0103_curation_local_assist.down.sql`、`0103_curation_local_assist.sql` |

### 验证结果

第 1 至第 7 步列出的 8 条定向 Go 命令、5 条非固定样本集成命令，以及 `git diff --check` 已通过。Go 命令从 `backend` 执行，使用计划列出的包和 `-run` 表达式。集成脚本首次直接访问 Docker socket 时权限不足，随后用本机已有 docker 组运行，形式为 `ROOMUSIC_TEST_RUN='<本步过滤器>' sg docker -c 'mise exec -- ./scripts/test-integration.sh'`。遇到仓库测试锁时等待并重试，每次实际执行都使用脚本创建的隔离临时库。

| 位置 | 已运行用例 |
| --- | --- |
| Go | 迁移回退文件配对、旧编码解码、混放与平票与编号、碟父目录与连续性、脚本引用、模型消息、指标驱动、包依赖门禁 |
| PostgreSQL | 迁移新装与再运行、整张读取旧编码、无标签编号目录、本地模型只写原文且保留曲序、曲目自动层撤销、目录扫描范围、手动并入后重扫、只升本地规则版本无外部工作 |

调整的既有断言共两处。

1. `TestMetricsOraclePatrolAndLocalValues` 检查 `texts` 中只有可见标题“桦窗册”，隐藏艺人无提交。原目录夹具也采用本轮目录结构。
2. `TestPostgreSQL整理无标签编号目录中的专辑名与艺人` 在 `releases[0].fields` 核实同次请求含 `title`、`album_artist`。模型调用次数、数据库自动值、操作来源和级别继续由该用例核对。

### 后续验收分工

- 测试代理按前文八组新用例补测七种入口、四类核对正反例、同艺人相邻独立专辑、配置与首次查询顺序、拆分和归组的撤销与恢复、旧库升级、动态引用。新增结构计划的实际消费与撤销、署名段的完整正反例、有数据的降级演练均在此清单中，本轮既有用例不覆盖这些场景。
- 主代理合入时运行固定样本子集及全套回归，记录基线差值；本轮没有固定样本性能或正确率结果。
- 前端治理记录需接 `local_model_grouping`。在 `frontend/src/features/governance/api/decoders.ts` 登记操作类型和 `changes.kind`，处理 `previous`、`assignments` 两组来源分配。没有旧分配时为 null。在 `frontend/src/features/governance/change-summary.ts` 增加“模型归组”摘要，撤销预览的 `grouping` 展示每组文件、碟号和曲号。本轮沿用现有主模型设置。

## 合并程序识别后的验证

`program.searchRelease` 在进入规则 105 的识别流程前检查本地依赖。无模型时直接进入该流程。保留规则 105 的七步查询、识别结论与普通未匹配 90 天重查。

`program.planCreditSplits` 在编号依据、原署名齐全而姓名与连接词拼不回时，收集目标、角色和未裁剪的原文。新识别和存量匹配补办都在各自的匹配结算事务内调用本地投影。已有 `TestProgramCreditSplitPlans` 的不同连接词样例现在同时核实交回的三个角色及原文。

0103 首次往返暴露了新操作类型缺回退夹具。补入模型归组和撤销记录后，带数据的 down 又发现删除旧操作产生的延迟约束检查尚待执行。已在更换操作类型约束前执行 `SET CONSTRAINTS ALL IMMEDIATE`。主代理将迁移顺延到 0104 时，留意同时顺延 `rollbackFixtures` 的 103 条目。

以下验证均已通过。Go 命令从 `backend` 运行。

```sh
mise exec -- go build ./...
mise exec -- go vet ./internal/curation/program ./internal/platform/postgres
mise exec -- go test ./internal/app ./internal/platform/postgres -run '^(TestPackageDependencyDirections|TestEmbeddedMigrationRollbackFiles)$'
mise exec -- go test ./internal/curation/program -run '^(TestProgramCreditSplitPlans|TestProgramRuleUpgradeStatePolicy)$'
```

集成命令从仓库根目录运行。

```sh
ROOMUSIC_TEST_RUN='^TestPostgreSQLMigrationRollbackRoundTrip$/^010[23]_' sg docker -c 'mise exec -- ./scripts/test-integration.sh'
ROOMUSIC_TEST_RUN='^(TestPostgreSQL整理本地模型只写本地片段并保留碟号曲序|TestPostgreSQL整理无标签编号目录中的专辑名与艺人|TestPostgreSQLTrackAutoRevert)$' sg docker -c 'mise exec -- ./scripts/test-integration.sh'
ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationLocalRuleUpgradeQuiet$' sg docker -c 'mise exec -- ./scripts/test-integration.sh'
git diff --check
git diff --cached --check
```
