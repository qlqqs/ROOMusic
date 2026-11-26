# 整理链 D 实施报告

## 核实结论与行为

- **艺人按规范化名字入库。** `backend/internal/catalog/ingest/persist.go:132` 保存扫描署名，`persist.go:191` 在同一事务刷新艺人图。`backend/internal/catalog/internal/artistgraph/names.go:86` 的 `nameResolver.ensure` 用 `textnorm.Text` 生成键，按版本和哈希查找，再比较完整键，仅为新键新建艺人。原拼写另存。因此没有人工区分时，同名的不同人共用一个本地身份。这项局限已写入本任务设计，供主任务汇总。
- **巡检**按当前稳定可见的库内名字增量处理。艺人名附最多三张专辑，各艺人的专辑名分别分块。每块最多 32 项，已巡过的相近专辑名可作为对照。结果保留原文、拉丁写法、置信度、问题、实际模型、提示词版本和时间。更换模型或提升提示词版本沿用已有结果，手工重巡可使其重新入队。高置信度艺人名按原文键或拉丁键归组，跨块也可关联。归组交 `verify_artist_merge`，明确错字与专辑异写交 `verify_name`，曲库写入沿用 E 的佐证规则。
- **识别**已接入 C 的 `CanonicalNames` 与 B 的 `NamesPatrolled`。文字搜索先用规范名，再按 C 的规则尝试原写法。新规范名会唤醒受影响且尚未匹配的发行，程序在保存旧搜索结论时检查巡检版本。
- **显示与搜索**沿用可见发行和曲目对各写法的去重票数。最高票并列时采用合并身份的有效外部主名；有人工名时优先显示人工名。自动合并优先选择有人工名的一方为目标。已有搜索投影覆盖合并双方的原写法、来源方人工名及最终显示名。缺少外部主名的旧合并，在相应来源可用时按原外部编号补齐。
- **本地整理**收集仍是占位值或含明显杂质的推断字段，按目录提供文件名、目录名和现有标签。扫描已正常解析的“01 - 艺人 - 曲名”跳过。后端在写入事务中复核来源和当前输入，只采用规范化后出现在某个本地片段里的值。通过既有治理入口按模型来源、`writer_level=1` 和 `origin.mode=local_cleanup` 写入。曲序和碟号没有提交字段。文字搜索及其候选重放可用整理结果，推断文字的认定仍须通过时长核对。强标识和声纹路径沿用扫描证据。

## 接口

| 调用方 | 接口 | 请求与成功响应 |
| --- | --- | --- |
| 管理员设置页 | `POST /api/v1/admin/curation/repatrol` | 同源管理员会话，无请求体。返回 `202 Accepted` 空体。已排队或正在运行的旧巡检项失效，稳定名录由下一轮调度重巡。 |
| 后台巡检 | `submit_patrol` | 必填 `item_id`、`results`、`issues`。艺人批的每一项提交 `{id,canonical,confidence}`，其中 `canonical` 为“原文名 \| 拉丁写法”，`confidence` 为 `high` 或 `low`。专辑批提交空 `results`。每条问题为 `{id,kind,suggested}`，`kind` 取 `artist_alias`、`typo` 或 `album_variant`。成功返回 `{"status":"handled"}`。 |
| 后台本地整理 | `submit_local_cleanup` | 必填 `item_id`，可提交 `title`、`artist` 和 `tracks:[{track_id,title}]`。只有可整理且能在本地材料中找到的字段才会采用。没有可用值时只传 `item_id` 即可结项。成功返回 `{"status":"handled"}`。 |

## 迁移与文件

- `backend/migrations/0092_curation_name_patrol.sql` 与对应 `.down.sql` 新建巡检结果、发行名录归属及索引。
- `backend/migrations/0093_curation_patrol_queue.sql` 与对应 `.down.sql` 加入巡检和本地整理两种待定项，调整开放项约束与索引，并添加 `curation_work_states.patrol_revision`。
- `backend/migrations/0094_merged_artist_names.sql` 与对应 `.down.sql` 添加 `external_links.artist_primary_name` 与合并家族有效主名视图。正向迁移随隔离集成数据库启动执行。降级迁移未单独演练。

下表的文件名均相对于该行目录。

| 目录 | 文件 |
| --- | --- |
| `backend/internal/app/` | `curation_model.go`、`curation_patrol_prompt.go` |
| `backend/internal/catalog/` | `curation_evidence.go`、`curation_artist_names.go`、`governance/artist_merge.go`、`internal/artistgraph/profiles.go` |
| `backend/internal/curation/` | `patrol.go`、`patrol_submit.go`、`local_items.go`、`local_submit.go`、`local_search.go`、`artist_link.go`、`artist_link_revert.go`、`external_link.go`、`external_link_revert.go`、`links.go`、`release_identity.go`、`pending_items.go`、`model_queue.go`、`work_states.go`、`writer.go`、`verification.go` |
| `backend/internal/curation/program/` | `worker.go`、`pass.go`、`queue_projection.go`、`queue_query.go`、`queue_artists.go`、`artist_links.go`、`artist_media_pass.go`、`artist_primary_name.go`、`match.go`、`match_search.go`、`match_snapshot.go`、`model_matcher.go` |
| `backend/internal/curation/` 子包 | `httpapi/handlers.go`、`model/item.go`、`model/scheduler.go`、`stewardtools/tools.go`、`stewardtools/patrol.go` |
| 本任务目录 | `design.md`、`report.md` |

## 实现代理的验证

最终代码在 `backend/` 下执行以下命令，均通过。app 单元集包含 `TestPackageDependencyDirections`。改动 Go 文件的 `gofmt -l` 与仓库的 `git diff --check` 均无输出。

```sh
mise exec -- go build ./... && mise exec -- go vet ./...
mise exec -- go test ./internal/curation/... ./internal/catalog/... ./internal/app/...
```

在仓库根目录执行 `sg docker -c 'ROOMUSIC_TEST_RUN="…" mise exec -- ./scripts/test-integration.sh'`。下表每行是一次运行；测试名用 `|` 拼接并限制为整名匹配。各次均使用脚本独立创建并清理的测试数据库。

| 运行 | 用例 | 结果 |
| --- | --- | --- |
| 本分支，队列与扫描 | `TestPostgreSQL整理队列五档取项与模型查询门槛`、`TestPostgreSQL自动写入四级先后矩阵`、`TestPostgreSQLCurationModelStartsCurationConversation`、`TestPostgreSQLCurationAuthenticityFromScan`、`TestPostgreSQL整理两边专辑匹配后用别名合并艺人` | 前三项通过，后两项失败 |
| 基线 `3fcf1a5c` 的临时副本 | 上行失败的两项 | 两项均复现相同失败 |
| 本分支，稳定夹具 | `TestPostgreSQLCurationAutoMergeSameMusicBrainzArtist`、`TestPostgreSQLArtistGraphScopedNamesPreserveRoutes`、`TestPostgreSQL整理进行中新专辑优先` | 三项通过 |
| 最终代码，模型调度和时长反证 | `TestPostgreSQLCurationModelStartsCurationConversation`、`TestPostgreSQL整理相同曲数的时长反证不能写入` | 两项通过，含审查拒绝及批准仍有反证两个子例 |

**本分支共 9 个不同集成用例，7 个通过，2 个基线失败。** `TestPostgreSQLCurationAuthenticityFromScan` 的对照发行仍为 `pending`、出站记录为空。 `TestPostgreSQL整理两边专辑匹配后用别名合并艺人` 同样停在发行识别之前。这两个夹具推进了程序时钟，未使 PostgreSQL 中的扫描静默时间达到 60 秒并完成确认扫描，因此未进入预期的匹配分支。原断言未修改。四次集成运行的日志位于本工作区的 `temp/archive/2026-11-25/d-patrol-*.log`。

## 测试代理验收

| 用例 | 断言 |
| --- | --- |
| `TestPostgreSQL整理增量巡检与管理员重巡` | 首轮实际巡检和持久化规范原文、拉丁写法、置信度、问题、模型和提示词版本。原样重扫的巡检用量增量为零。换模型和提高提示词版本都沿用已保存结果。新增名字和改过的专辑标题进入巡检。管理员重巡更新了当前名录的记录，成员和跨来源请求被拒绝。高置信度名字跨批产生 `verify_artist_merge`，专辑错字产生 `verify_name`，低置信度名字排除在归组外。巡检和未获佐证的核对均无曲库写入。 |
| `TestPostgreSQL整理规范名搜索并合并艺人` | V6。两侧初始为独立本地艺人。一侧在巡检前无版本对应，巡检提供专辑规范名后由程序搜索并建立对应，两个艺人的合并来源为 MusicBrainz。D5 覆盖平票取外部主名、多数票取库内写法、人工显示名优先。各原始名字和显示名均可搜索。 |
| `TestPostgreSQL整理无标签文件名直接识别` | V16。扫描先从文件名提取艺人和曲名，程序使用这些推断值查询发行并按曲目时长配对，补写厂牌和曲目对应。该发行进入本地整理及模型识别的次数均为零。 |
| `TestPostgreSQL整理本地模型只写本地片段并保留碟号曲序` | V16。受控模型同时提交真实目录、文件片段和编造值。前者以模型推断、`writer_level=1` 保存并供搜索使用，后者均无写入。扫描的曲序和碟号逐曲相同。 |
| `TestPostgreSQLCurationMetrics` | V12。真实巡检通过 B 的 `NamesPatrolled` 供第二档领取。度量按项检查模型自查和联网的 D16 上限，并检查标为程序可定的发行或录音进入模型识别的数量。 |
| `TestPostgreSQLAPIResponseSamples` | 管理员重巡是 HTTP 202 空体，前端样本以 `null` 表示空体。诊断样本的迁移版本更新为 94。 |

度量驱动和通用上游 mock 均识别巡检与本地整理工具。通用 mock 默认返回低置信度身份或空整理结果。M 使用合成清单中的别名和错字产生巡检回复，统计两个新类别的调用及用量。本地文字接受和拒绝由专门的 V16 用例提供正反输入。

### 全量整理集成初跑与分类

初跑使用 `TestPostgreSQL(Curation|整理|ArtistWebSearch|ArtistMedia|ArtistLink|TrackAuto|B3)`，共 174 个顶层用例。148 个通过、26 个失败、0 个跳过。下表按初跑的主要失败原因归类。同一用例可同时涉及扫描确认和模型合同等多项变化。

| 原因 | 初跑失败的用例 | 测试处理 |
| --- | --- | --- |
| B 的 D7 扫描确认 | `TestPostgreSQLCurationLocalRuleUpgradeQuiet`、`TestPostgreSQLCurationAuthenticityFromScan`、`TestPostgreSQL整理发行组对应的封面归组和撤销`、`TestPostgreSQL整理散放单曲的写入粒度`、`TestPostgreSQL整理两边专辑匹配后用别名合并艺人`、`TestPostgreSQL整理零候选按来源和模型分流`、`TestPostgreSQL整理未收录九十天与管理员重查`、`TestPostgreSQLCurationReadOnlyMusicTree`、`TestPostgreSQLCurationScannedAlbumProvenance`、`TestPostgreSQLCurationStreamingSingleEnrichment` | 推进隔离库的扫描静默时间并通过第二次扫描确认。识别断言限定相应的匹配项，单曲 ISRC 夹具改用 C 的录音搜索合同。 |
| B 的艺人识别门槛、A/C 的版本和排队合同 | `TestPostgreSQLArtistWebSearchMediaPrecedence`、`TestPostgreSQLCurationAutoMergeReplay`、`TestPostgreSQLB3LegacyWorkReopenedOnce`、`TestPostgreSQLCurationExternalRuleUpgradePending` | 资料优先级夹具补发行识别结论。回填状态按主题检查。B3 使用当前摘要和版本隔离旧的无指纹结果，按 C 的搜索和未收录规则检查重开。零候选升级分别验证离线登记事务和后续补查。 |
| E 的选择、审查、分流和建议合同 | `TestPostgreSQLCurationExceptionAcceptDecline`、`TestPostgreSQLCurationToolsPending`、`TestPostgreSQLCurationReleaseMergeProposed`、`TestPostgreSQLCurationUnavailableReviewPreservesWrites`、`TestPostgreSQLCurationPendingUpsert`、`TestPostgreSQLCurationShelvedReopensOnNewCandidates` | 用受控巡检响应后再观察第一档，放弃工具验证当前档结项，短专辑匹配注入已批准的测试审查收据。独审不可用用例改走选择到审查。旧 `waiting_human` 持久化测试改为分流项输入变化及路由复位，拒绝建议后保留 E 的结项原因。 |
| D 增加待定项与模型工具 | `TestPostgreSQLArtistWebSearchMockMediaAndUsage`、`TestPostgreSQLB3AcoustIDProgram`、`TestPostgreSQLCurationProgramLibrary`、`TestPostgreSQLCurationModelMergeReleases`、`TestPostgreSQLCurationMetrics` | 联网用量和总用量按巡检差额校验。B3 和曲库用例按识别项种类读取候选。后台工具清单补入两种提交。M 驱动解析巡检证据、提交结果并推进巡检触发的下一轮程序识别。 |
| 记录建议后合并艺人 | `TestPostgreSQLArtistWebSearchProgramScope` | 待定项按 E 的 `resolved/suggestion_recorded` 检查。保留原来对开放建议收尾和保留采纳中建议的断言。 |

### 复跑结论

合并初跑及后续按需复跑，每个顶层用例以最后一次结果计。指定范围内共 178 个用例，**177 个通过、1 个失败、0 个跳过**。新增的四个 D 验收用例均通过。范围外另跑的 `TestPostgreSQLAPIResponseSamples` 通过，覆盖迁移 0094 后的诊断样本和管理员重巡空响应。初跑中指定的两个基线失败 `TestPostgreSQLCurationAuthenticityFromScan` 与 `TestPostgreSQL整理两边专辑匹配后用别名合并艺人` 均在更新 D7 扫描夹具后通过。

`TestPostgreSQL整理零候选按来源和模型分流` 的旧领取断言已改为验证真实 `LookupTierReady` 和按 `lookup` 类别调用 `ClaimModelKind`。名字未巡检或程序高优先项未排空时保留待领取状态。两道门槛满足后领取指定零候选项，并验证过期摘要被拒绝、正确摘要写入 90 天复查时间。

| 分类 | 最后一次失败 | 实测与原因 |
| --- | --- | --- |
| 生产缺陷 | `TestPostgreSQLArtistWebSearchProgramScope` | E 在写建议时将原待定项置为 `resolved/suggestion_recorded`。随后合并艺人，程序只扫描尚未结清的艺人待定项，`ResolvePendingItems` 也只收尾这次结清的项所挂的开放建议。复跑中已并入身份上的开放建议未置为 `superseded`，与艺人搜索合同的合并收尾要求冲突。采纳中的建议仍保留原状态和决定人，符合合同。生产处理留待修复。 |

### 命令与结果

| 验证 | 命令或过滤范围 | 结果与日志 |
| --- | --- | --- |
| 全量整理初跑 | `sg docker -c 'ROOMUSIC_TEST_COMPOSE_PROJECT=roomusic-compose-test-d-baseline ROOMUSIC_TEST_RUN="TestPostgreSQL(Curation\|整理\|ArtistWebSearch\|ArtistMedia\|ArtistLink\|TrackAuto\|B3)" mise exec -- ./scripts/test-integration.sh'` | 148 通过、26 失败，见 `temp/d-tests/baseline.log`。 |
| 重点复跑 | 通过 `sg docker -c 'mise exec -- ./scripts/test-integration.sh'`，用 `ROOMUSIC_TEST_RUN` 整名筛选初跑失败、新增验收及 API 样本，具体重跑名单见 `temp/d-tests/retest-pattern` | 27 通过、3 失败，见 `temp/d-tests/retest.log`。 |
| 最后一轮重点复跑 | 同一集成入口，`ROOMUSIC_TEST_RUN` 覆盖 `TestPostgreSQLArtistWebSearchMock.*`、`TestPostgreSQLArtistWebSearchProgramScope`、`TestPostgreSQLCurationReadOnlyMusicTree`、`TestPostgreSQL整理零候选按来源和模型分流` 及四个 D 用例 | 8 通过、2 失败，见 `temp/d-tests/final-check.log`。 |
| 第二档领取用例补跑 | 同一集成入口，`ROOMUSIC_TEST_RUN='^TestPostgreSQL整理零候选按来源和模型分流$'` | 顶层及各子例均通过，8.14 秒，见 `temp/d-tests/lookup-gate.log`。该 Compose 项目的容器、卷和网络已清理。 |
| 后端全量单元 | `env -u ROOMUSIC_TEST_DATABASE_URL mise exec -- go -C backend test ./...` | 通过，数据库用例由集成入口验证，见 `temp/d-tests/unit-final.log`。 |
| Go 静态检查 | `mise exec -- go -C backend vet ./...` | 通过，见 `temp/d-tests/vet.log`。 |
| 前端 API 契约 | `mise exec -- npm --prefix frontend run test -- src/test/api-contract.test.ts` | 821 通过，1 个仓外大样本未配置而跳过，见 `temp/d-tests/frontend-final.log`。 |
| 补丁检查 | Go 测试文件和 mock 的 `gofmt -l`，以及 `git diff --check` | 无输出。 |
| M 两种模式 | 同一集成入口，`ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$'`，分别设 `ROOMUSIC_CURATION_METRICS_MODEL=oracle` 和 `first_candidate` | 两次通过，分别用时 466.96 秒和 490.17 秒，见 `temp/d-tests/oracle-final.log` 与 `temp/d-tests/first_candidate-final.log`。 |

两种 M 模式的错连、错并、错改都是 0，别名均完成 2 项。Oracle 的错字完成数保持 3，First candidate 从 0 增至 3。两种模式原样本标注为程序可定却进入模型识别的数量都是 0。逐项用量低于 D16 上限。两种模式各调用巡检 180 次、本地整理 12 次。详见[度量报告](research/metrics-2026-11-25.md)和[父任务基线](../11-25-curation-chain/research/m-baseline/after-d/)。

已按集成日志确认两个最终度量与最后一轮重点复跑结束，并核对所有本任务 Compose 项目下的容器、卷和网络均已清理。所有模型请求发往测试回环端点。
