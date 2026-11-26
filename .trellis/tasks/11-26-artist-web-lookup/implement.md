# 整理艺人实施顺序

实施沿用既有用例，新增的合成场景交由测试代理补齐。命令从仓库根运行，Go、Node 和集成脚本使用仓库的 mise 版本，集成测试使用独立测试库、临时目录和模拟上游。

## 实施步骤

1. **建立手动队列存储与状态接口。**

   修改 `backend/migrations/0105_artist_web_lookup.sql`、对应 `.down.sql`、`backend/internal/curation/artist_web_queue.go`，为 `curation_pending_items` 增加艺人网页代次列并建立 `curation_artist_web_queue` 单行控制表。实现单艺人加入、全量加入、停止、接续和状态读取，复用现有艺人证据摘要与 `UpsertPendingItem`。不在这一步发送模型或网页请求。

   风险是迁移编号与子任务 5 冲突、旧自动项被误加入新代次、重复点击创建重复项。领取条件必须同时检查代次、队列状态、艺人仍无 MusicBrainz 对应和输入摘要。

   ```sh
   (cd backend && mise exec -- go test ./internal/curation -run '^Test(ArtistWebQueue|ArtistWebInputDigest|PendingItemArtistScope)$')
   (cd backend && mise exec -- go test ./internal/platform/postgres -run '^TestEmbeddedMigrationRollbackFiles$')
   ROOMUSIC_TEST_RUN='^TestPostgreSQLMigrationRollbackRoundTrip$/.*artist_web_lookup' mise exec -- ./scripts/test-integration.sh
   ```

2. **移除自动创建并接入手动排序。**

   修改 `backend/internal/curation/program/artist_media_pass.go`、`program/queue_artists.go`、`backend/internal/curation/artist_web_queue.go`和 `backend/internal/curation/model_queue.go`。删去资料队列为无对应艺人创建联网项的分支，保留已对上艺人的图片和简介处理。实现专辑艺人优先、合辑曲目艺人在后的候选排序，并在模型 SQL 中只领取当前手动代次的 `artist_identity`。

   风险是把已有自动资料项误当作新手动项、把合辑艺人重复排队，或让整理链等待艺人项。整理主循环不能调用手动队列入口，艺人队列停止时发行和资料队列仍可运行。

   ```sh
   (cd backend && mise exec -- go test ./internal/curation/program -run '^Test(ArtistWebQueueOrder|ArtistMediaDoesNotCreateWebItem|ArtistWebSkipsUnchangedInput)$')
   ROOMUSIC_TEST_RUN='^TestPostgreSQLArtistWebSearch(ProgramScope|QueueOrder|DoesNotAutoCreate|ResumeUnchanged)$' mise exec -- ./scripts/test-integration.sh
   ```

3. **接入共享模型额度、暂停和单项上限。**

   修改 `backend/internal/curation/model/item.go`、`model/queue.go`、`model/scheduler.go`、`backend/internal/curation/model_usage.go` 的调用方和 `model_failures.go` 的状态读取适配。独立艺人队列在领取和每次新请求前调用 `CheckDailyBudget`，使用同一个 `ModelState`，不检查整理开启者。为 `artist_identity` 固定 10 次工具调用和 50000 token，实际 usage 通过 `RecordItemUsage`，工具次数通过 `AddItemCalls`，达到上限则保留候选并结束当前项。

   风险是复用 web 路线的 30 次和 300000 token 默认值、重试未计数、每日额度检查发生在建会话之后，或停止时把在途 usage 丢掉。先写上限判定的纯函数，再接调度器边界，保持既有 release web 路线的限制。

   ```sh
   (cd backend && mise exec -- go test ./internal/curation/model -run '^Test(ArtistWebItemLimit|ArtistWebDailyBudget|ArtistWebStopKeepsInFlightUsage)$')
   ROOMUSIC_TEST_RUN='^TestPostgreSQLArtistWebSearch(SharedDailyBudget|ItemLimits|ModelPause|RetryAfterQuota)$' mise exec -- ./scripts/test-integration.sh
   ```

4. **实现候选提交、结构核对和直接写入。**

   修改 `backend/internal/curation/stewardtools/artist_identity.go`、`curation/program/artist_identity.go`、`program/artist_links.go`、`program/match_album.go`、`program/match_snapshot.go`、`curation/external_link.go` 和 `curation/artist_media_suggestion.go`。只接受 MusicBrainz MBID，使用子任务 5 合入后的结构匹配器。结构吻合时通过既有 Writer 和治理事务同时写艺人及发行对应，失败回滚。结构不吻合时生成无审查 `open` 候选，沿用现有确认和否掉入口。

   风险是只写艺人链接而漏掉那张发行、直接插表绕过操作记录、把模型网页文字当事实、或让旧审查候选走无审查分支。按候选来源和待定项代次区分新旧路径，写入前重新加载艺人、发行和输入摘要。

   ```sh
   (cd backend && mise exec -- go test ./internal/curation/program -run '^Test(ArtistIdentityStructureMatch|ArtistIdentityRejectsMismatch|ArtistIdentityCandidateReuse)$')
   (cd backend && mise exec -- go test ./internal/curation/stewardtools -run '^Test(ArtistIdentityTool|ArtistIdentityCitations|ArtistIdentityNoReview)$')
   ROOMUSIC_TEST_RUN='^TestPostgreSQLArtistWebSearch(StructureMatchWritesRelease|CandidateNeedsHuman|DeclineCandidate|StaleCandidate)$' mise exec -- ./scripts/test-integration.sh
   ```

5. **提供配置可见性和 HTTP 状态。**

   修改 `backend/internal/curation/httpapi/artist_media.go`、`backend/internal/curation/httpapi/handlers.go`、`backend/internal/curation/source_finders.go` 或其现有配置读取位置，以及必要的 `backend/internal/app` 装配。扩展艺人页 GET 状态，新增单艺人 POST、全量 GET/POST/DELETE 路由，返回配置、动作能力、队列进度、单项次数和候选。网页搜索配置独立于整理开关，错误使用 `search_unconfigured`、`model_not_configured`、日额度和模型暂停的已有分类。

   风险是把未登录成员的配置状态泄露到 403 之前、把整理关闭误报为搜索未配置、或状态读取和写入使用不同的有效艺人。所有路由先做管理员权限和有效身份归一，再读取状态，响应不含密钥、模型端点或网页原文。

   ```sh
   (cd backend && mise exec -- go test ./internal/curation/httpapi -run '^Test(ArtistWebSearchRoutes|ArtistWebSearchState|ArtistWebSearchAuthorization)$')
   ROOMUSIC_TEST_RUN='^TestPostgreSQLArtistWebSearch(HTTP|SourceRequirements|ManualStartStop|QueueProgress)$' mise exec -- ./scripts/test-integration.sh
   ```

6. **同步规范和接口样本。**

   修改 `.trellis/spec/backend/curation-contracts.md`、`.trellis/spec/backend/steward-web-contracts.md`、`.trellis/spec/backend/artist-contracts.md`、`.trellis/spec/backend/steward-config-contracts.md`，以及需要更新的后端 API 样本和前端交接说明。写清手动触发、队列状态、结构核对、不审查、共享额度和 10 次/50000 token 上限。前端由主代理根据本设计更新 decoder、hooks 和图标按钮。

   风险是仍保留“整理自动建联网项”、旧的独立联网额度或“候选必须审查”表述，造成代码与规范冲突。只替换被 ADR 0009 第 7 节取代的段落，保留媒体来源和人工对应合同。

   ```sh
   rg -n '自动.*联网|web_daily_token_limit|需要独立审查|artist_identity' .trellis/spec/backend/{curation-contracts,steward-web-contracts,artist-contracts,steward-config-contracts}.md
   git diff --check
   ```

7. **定向验收和依赖门禁。**

   实现与测试合入后，运行艺人联网相关的单元和集成用例，确认不点击按钮没有 web_search/fetch_page 出站，单艺人和全量顺序正确，停止可恢复，日额度共用，结构吻合能同时认出艺人和发行。最后运行依赖方向门禁，不扩大到全量真实模型轮。

   ```sh
   ROOMUSIC_TEST_RUN='^TestPostgreSQLArtistWebSearch' mise exec -- ./scripts/test-integration.sh
   (cd backend && mise exec -- go test ./internal/app -run '^TestPackageDependencyDirections$')
   git diff --check
   ```

## 测试代理补测清单

测试代理只改测试文件和合成数据，不修改生产代码。每条用例使用模拟 MusicBrainz、模拟网页搜索和脚本模型，覆盖以下断言。

| 范围 | 对照样本和断言 |
| --- | --- |
| 自动行为 | 未点击任何入口且启用整理时，不创建新 `artist_identity` 项，不发 web_search 或 fetch_page。已有历史自动项可读但不被新调度器领取。 |
| 手动排队 | 两张普通专辑艺人和一张合辑的三位曲目艺人按“专辑艺人在前、合辑各曲艺人在后”领取。重复点击不增加项。已对上艺人、合并艺人和摘要不变艺人被跳过。 |
| 停止和恢复 | 队列有等待项和一个在途项时停止，只让等待项留队，在途项正常记账。重新开始接续相同代次。输入摘要变化后重新排队并把该项次数归零。 |
| 共享额度 | 整理项先消耗日额度后，艺人项不再建会话。艺人项消耗后，整理项同样看到余额。跨日和调高额度可继续，已发请求的实耗完整进入日账。 |
| 单项上限 | 第 10 次工具调用和 50000 token 分别触发停止，重试也计入，候选仍可读取。一个艺人达到上限不阻塞其他艺人。 |
| 单次实耗越界 | 最后一次请求真实用量高于剩余额度，完整实耗记入日账和该项，下一个请求没有发出。 |
| 缺报用量 | 首次及重试均无 usage，本项每次按完整输入估算加受限输出上限累计，日账保持实际值，余量耗尽停止发送。 |
| 结构核对 | 候选 MBID 对应的一张发行曲数和时长吻合时，同一事务产生艺人及发行对应。曲数不符、时长超容差、候选艺人类型不合法时不写任何对应。 |
| 候选确认 | 结构不吻合时返回候选，确认重新核对后写入，否掉写抑制。候选或本地摘要变化时确认返回 `exception_stale`。不创建审查方案。 |
| 配置显示 | 未配模型、未配网页搜索、搜索来源关闭和整理关闭分别返回正确的 `configured` 与错误原因。整理关闭不阻止已配置的独立艺人队列。 |
| 治理和回滚 | 直接写入有操作、来源和批次，可按现有操作撤销。迁移 up/down/up 保留旧待定项、候选、对应和共享日账。 |

固定样本之外只运行本步骤涉及的包和用例。未执行的真实模型试用和全库轮次不列为通过。

## 前端交接

| 操作 | 接口与返回 |
| --- | --- |
| 单艺人状态和查找 | `GET/POST /api/v1/artists/{id}/web-search`。POST 无请求体，成功 202。读取 `configured`、`can_start`、`can_stop`、`state`（none、queued、running、shelved、waiting_human）、可空 `queue.{generation,position,total}`、`usage.{tool_calls,tokens,tool_call_limit,token_limit}`、`candidates`。 |
| 全量队列 | `GET/POST/DELETE /api/v1/admin/curation/artist-web-search` 分别读取、开始或接续、停止。返回 `configured`、`can_start`、`can_stop`、`state`（idle、running、stopped）、`generation,total,queued,running,processed,retry_at`。POST 成功 202，其余成功 200。达到共享日额度时未处理项保留，`retry_at` 为下一个本地日界。 |
| 显示和权限 | 接口均需管理员。模型和网页搜索就绪时 `configured=true`，按钮的可操作性取自 `can_start/can_stop`。未配置的 POST 返回 409 和 `model_not_configured` 或 `search_unconfigured`，来源关闭返回 `source_disabled`。单人启动只释放该人的停止标记。 |
| 身份候选 | 候选含 `exception_id,source,external_id,name,description,note,citations`，引用含 `url,title`。对该 ID 调用 `POST /api/v1/admin/curation/exceptions/{id}/accept` 或 `/decline`，成功返回 `{exception,operation_ids}` 或 `{exception}`，随后刷新艺人状态。 |

## 已有用例与夹具的调整

1. `ProgramScope` 改为检查程序轮结束后没有身份项，显式 POST 后再检查有效署名、证据范围和已有历史候选。
2. `ProgramPluginName` 继续验证插件写入，移除资料缺口会自动产生身份项的预期。
3. `IdentityRejectionsAndStates` 通过艺人页启动，保留越界引用、无效类型、抑制、权限和无审查断言。名称差异不能单独推翻结构证据。
4. `MusicBrainzWithoutType` 对有效且无结构佐证的候选执行人工采纳，检查治理操作。
5. 原网页媒体校验、未证实的媒体出处两例改为 `HistoricalMediaValidation`、`UnverifiedMediaSource`，分别验证历史建议的合法采纳、格式及引用校验和私网资源不出站。
6. `MediaCompletesSingleField` 改为验证身份队列无法提交图片或简介。历史网页图片与简介的抑制、优先级和撤销用例改用显式历史领取夹具。
7. `MockIdentity` 使用曲数、曲序和时长吻合的合成专辑，断言无审查写艺人及发行对应，下一轮继续补充有许可的资料。
8. `MockMediaAndUsage` 的模拟模型仅找到资料，调用放弃工具。检查共享日账按实际模型请求累计并验证没有写艺人资料。
9. `MockCandidates` 的两位同名候选使用不同 MusicBrainz 编号，无审查地保留两个候选，检查确认、并发确认、否掉、占用和对同源候选的抑制。
10. 原「整理艺人联网无结果后程序队列排空」改名 `NoResultDoesNotRestart`，显式创建并领取手动项，再跑完整盘点窗口检查相同输入没有重新排队。

## 实现验证结果

- 第 7 步的艺人联网集成筛选通过，共 15 个顶层用例，含子用例。依赖方向门禁与第 1 步嵌入迁移回滚文件检查通过。
- 艺人联网队列的正向和回滚迁移号为 0105。
- 第 2、5 步单独运行的集成筛选分别命中 ProgramScope、SourceRequirements。第 3、4 步专项集成筛选没有命中用例。第 1 至 5 步针对新合同命名的单元测试尚未由测试代理加入，包编译通过。测试代理继续补齐不同配置和额度、队列启停与并发、同组与跨组等对照样本。
- 规范检索及 git diff --check 完成。前端改动归主代理，工作区保留未提交改动。
