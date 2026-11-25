# 整理链 E 实施与验证报告

2026-11-25。后端已实现三档识别、核对判断、审查策略 v5、联网子额度与未采用建议。构建及静态检查通过。现有单元和集成用例的实测差异列在下文。V17 的 92 项受控度量留给测试代理。

## 运行与写入

| 入口 | 后端行为 |
| --- | --- |
| 第一档选择 | 同一类发行或录音默认最多 5 项。按编号补齐完整候选，首条列本地曲目、时长、搜索及候选资料。仅发本批一次主模型请求，工具列表为空，原生搜索关闭。校验每项唯一、集合完整且选定编号确在本项；升级进入 lookup。沿用 1 MiB 消息边界，按整项拆包，单项仍过大则升级。 |
| 第二档自查 | 消费第一档升级与 C 的 lookup 路线。在 B 的高优先项排空、来源可用、发行稳定且名字已巡检后领取。同艺人一组，在项边界逐项开始。允许 MB 查询、读取和提交候选外编号。 |
| 核对判断 | 消费有效的同位署名提案、结构确认后的名称差异，以及供 D 交入的身份提案。后台读取原值、拟改值及可取得的佐证。错字才可写入，译名、风格差异保留；结构合并继续由专用入口预览。 |
| 联网兜底 | 沿用管理员的网络搜索开关，优先处理在库可见专辑组更多的艺人。网页引用须来自本项的搜索结果或原生引用，后端取正文核实。搜索、取页、提交身份、媒体或证据的白名单按项种类开放。 |
| 结项及停止 | 自查每项 20 次工具调用或 150000 token，联网每项 30 次或 300000 token。每次请求结束按实际 usage 和工具调用累计，超过后停止新调用并按本档放弃。原生搜索按上游报告计数。项边界检查总日额、联网子额；运行中继续检查开关、开启者、暂停与扫描。审查暂不可用会保留方案有限重试，模型服务错误释放领取并退避。 |

选择或提交的编号都由后端重新读取。专辑的碟、位置和曲数须匹配，超过四首且至少一对时长可比，所有可比时长都在容差内方可免审；单曲核实标题、艺人和时长。无效编号、无法配对的碟或曲位、已有冲突对应仍由预览入口拒绝。审查后的写入携带审查 ID，后端复判无独立证据可解释的时长或真实文字反证。操作记录保留核对方式及适用的审查来源。

核实外部值的模型写入用 `writer_level=3`。库内或网页佐证用 2，来源分别为 `library_evidence`、`web_search`，沿用批次和按来源撤销。艺人合并在受治理事务内复核历史同曲并列署名、人工分开及撤销抑制。同一身份项的互斥候选逐条留档。真实名字纠正与按空字段补值分别处理。年份、厂牌、目录号、条码等事实需要开放库字段值；仅有推测时先转联网，仍无法核实则保留建议。识别无对应走 `FinishUnlisted`，由程序负责到期重查。

0091 沿用 `curation_model_exceptions`，将旧 `waiting_human` 结为 `resolved/suggestion_recorded`，保留旧建议 ID、审查与采纳记录。人工采纳沿用预览、版本检查、操作幂等及受治理的写入入口。down 恢复旧结构和状态约束，删除旧结构无法表示的新增种类或无审查建议。按审阅意见采用常规 down，无额外导出步骤。

## 前端接口

管理员前缀为 `/api/v1/admin/curation`。

| 请求 | 成功响应及含义 |
| --- | --- |
| `GET /settings`、`PUT /settings` | 设置对象顶层新增 `web_daily_token_limit: number \| null`。PUT 可只传这个字段，取值 1 至 1000000000000，显式 null 取消子限额，缺省维持原设置。新迁移默认 null。总额度仍由 `daily_token_limit` 控制。 |
| `GET /progress` | `model.web_daily_token_limit` 同设置，`model.web_today` 为 `{date,input_tokens,output_tokens}`，`model.web_limit_reached` 为布尔值，`model.web_retry_at` 为时间字符串或 null。达到子额度时记录下一个本地零时，其他符合条件的档仍可运行。联网用量已包含在 `model.today` 中。 |
| 进度计数 | `pending.waiting_model` 和 `model.items.waiting_model` 均含可见的 waiting_model 及 waiting_route 项。后者的同级 `waiting_human` 兼容返回 0。未采用建议单独分页。 |
| `GET /exceptions?status=open&page=1&page_size=50` | 默认 open；all 包括处理中和历史决定。返回 `{items,total,page,page_size}`，每页最多 100。行新增 `reason_code: string`、`reasons: string[]`、`evidence: JSON`、`can_accept: boolean`。`review` 可为 null；有审查时为 `{id,model,decision,source,risk,reasons}`。保留 `id,pending_item_id,tool,input,preview,kind,release,artist,conversation_id,status,decided_by,decided_at,operation_ids,created_at` 和可选 `track`。 |
| `POST /exceptions/{id}/accept`、`POST /exceptions/{id}/decline` | 采纳返回 `{exception,operation_ids}`，不采纳返回 `{exception}`。open 表示尚可查看并决定，`can_accept` 表示有可执行主张；执行前重新校验目标和版本。同项互斥身份可出现 superseded。继续沿用 exception_decided、exception_stale、nothing_to_apply 等错误。 |

建议原因包括 `review_rejected`、`review_uncertain`、`verification_declined`、`no_evidence`、`unverified_fact`。明确属于不同身份、写法或译名差异的结论以及命中合并硬性否决的项，以 `tool=observation, can_accept=false` 留档。界面按 `can_accept` 展示采纳入口，使用顶层 reasons 显示无审查建议的理由。

## 验证结果

在 `backend/` 执行 `mise exec -- go build ./...`、`mise exec -- go vet ./...` 均通过。已对改动和新增的 Go 文件检查 gofmt，并运行 `git diff --check`。

`mise exec -- go test ./internal/curation/... ./internal/steward/... ./internal/app/...` 在追加修复前返回 1。`internal/curation/...` 通过。下表记录当时的失败及复核结果。

| 现有用例 | 原因 |
| --- | --- |
| `TestBackgroundUsageAndReviewEscalation` | 仍要求 escalated_to_human；v5 在后台返回 rejected_by_review 并附建议 ID。 |
| `TestReviewPolicyRulesMatchSpec/ReviewPolicyRules`、`/ReviewEvidenceFields` | 源码已升 v5，主任务尚待同步的审查合同仍为 v4。逐字比对失败。 |
| `TestPackageDependencyDirections` | 已修复，定向复测通过。以整理链开始前的 `e25f9bb1` 为准，`faa6aaeb` 引入了 catalog 对 sources 和 catalog/metadata 的依赖，`ec0c726f` 引入了 upstreammock 对 catalog/textnorm 的依赖。 |

追加修复后，目录层返回自己的 `catalog.DiscTOC`，整理程序在查询来源时转换为 `sources.DiscTOC`。依赖允许表登记了 catalog → catalog/metadata 和 upstreammock → catalog/textnorm。在 `backend/` 依次执行 `mise exec -- go build ./...`、`mise exec -- go test ./internal/app -run '^TestPackageDependencyDirections$'`、`mise exec -- go test ./internal/catalog/... ./internal/curation/...`，全部通过。

在仓库根目录通过 `ROOMUSIC_TEST_RUN` 收窄，使用 `mise exec -- ./scripts/test-integration.sh`、独占 Compose 项目和独立临时目录。各轮已清理自己的数据库与网络，模型调用使用测试替身。

- 通过 `TestPostgreSQLCurationModelAPI`、`TestPostgreSQLCurationModelAPIMatchErrors`、`TestPostgreSQLCurationModelSettings`、`TestPostgreSQLCurationModelPlan`、`TestPostgreSQLCurationModelPlanAfterClaimRefresh`。
- 通过 `TestPostgreSQLCurationAutoMergeSameMusicBrainzArtist`、`TestPostgreSQL整理队列五档取项与模型查询门槛`、`TestPostgreSQLCurationModelUnconfiguredEnabler`、`TestPostgreSQLCurationModelRestartRecovery`。修正每日用量 SQL 参数类型后，`TestPostgreSQLCurationModelDailyUsage` 复测通过。
- `TestPostgreSQLCurationExceptionStore` 仍断言创建例外后父项为 waiting_human；实际为 resolved/suggestion_recorded。
- `TestPostgreSQLArtistWebSearchMockMediaAndUsage` 的旧模型脚本先读待定项并直接提交媒体。新后台从首条消息取得当前项，网页证据与独立审查参与提交。脚本三次没有完成有效提交后，该项被标为 unhandled。
- `TestPostgreSQLCurationModelApply` 直接对单曲目的发行调用 Apply，未提供独立审查。新写入口返回 approval_required。
- `TestPostgreSQLCurationModelReviewUnavailableBackoff` 的夹具只给候选标题，没有可选编号，未收到预期的第一档主请求。用例停在构造待审方案之前，本轮未端到端验证新协议的重审退避。

这些现有断言保持原样。集成实测范围仅包括上述用例，未执行 0091 down、全套集成或 M 验收。

## 合并与度量交接

D 提供 `NamesPatrolled` 前，生产调度器的 lookup 路线会等待；五档队列测试覆盖已注入巡检结果的情况。前端解码、合同与 ADR 同步由主代理负责。测试代理需按本次任务协议适配受控端点，复跑 M 的两个模式。

B 后基线中 first_candidate 在 6 张专辑共错连 38 条、错改 4 处。near 与 truncated 有逐轨时长反证，wrong-id 虽时长相符但专辑、艺人、真实曲名全面冲突。新路径在写任何衍生录音、艺人或字段前核对，审查模型一律批准也不能覆盖这些反证。验收目标为错连、错并、错改均为零，oracle 完成数至少 81/92，并逐项检查原来的 11 个 waiting_route；本轮未取得 after-E 实测数字。

## 测试代理验收（2026-11-25）

受控端点支持第一档严格 JSON，按首条消息与工具白名单识别第二档、联网、核对，提供合成 MusicBrainz 详情、网页正文与独立审查。API 实录更新了设置、进度、例外列表及决定响应，含联网子额度、带证据和理由的建议、可人工采纳标记及空审查。诊断样本的迁移版本同步到 91。

| 验收 | 通过的用例与断言 |
| --- | --- |
| V5 | `TestPostgreSQL整理审查未采用即结项` 验证拒绝和拿不准都结清、没有写入、进度 waiting_human 为 0。`TestPostgreSQL整理迁移旧待办为可采纳建议` 用 0091 down 建出旧待办后执行 up，验证原建议身份、理由和审查信息，并经 API 手动采纳。 |
| V7 | `TestPostgreSQL整理专辑名字只纠正有佐证的错字` 覆盖外部值、三级自动层、修正 API 同时返回原值与有效值、撤销，以及繁简、大小写、标点和外部特殊写法保留。 |
| V8 | `TestPostgreSQL整理模型记忆的事实只留建议`、`TestPostgreSQL整理无佐证主张先联网再留建议` 验证先转联网、取证失败后只留建议。`TestPostgreSQL整理无佐证不合并艺人与发行` 验证无外部、库内或网页佐证的艺人合并成为建议，无强依据时拒绝发行并入。 |
| V15 | `TestPostgreSQL整理第二档查到候选外编号直接写入` 检查第一档仅发完整候选且无工具，第二档门槛关闭时不请求模型，打开后查询、读取并提交候选外编号，五首结构核对通过时免审写入。`TestPostgreSQL整理相同曲数的时长反证不能写入` 验证独立审查拒绝和批准后反证仍在两种情况。`TestPostgreSQL整理第二档按本地艺人打包` 验证单批领取同艺人的发行。 |
| V17 与艺人佐证 | `TestPostgreSQL整理同曲并列署名否决艺人合并` 验证同一 MBID 也无法绕过否决。`TestPostgreSQL整理同位库内署名经审查可合并`、`TestPostgreSQL整理同位外部署名经审查建立对应` 检查库内及外部佐证的成功路径。M 的两种模式及 11 项路线见 [度量报告](research/metrics-2026-11-25.md)。 |
| 预算与开关 | `TestPostgreSQL整理联网额度与单项额度` 验证联网子额度只拦联网，以及第二档 20 次工具或 15 万 token、联网 30 次工具或 30 万 token 超限后的转档和结项。`TestPostgreSQL整理自查的摘要计入当天总额度` 保留摘要计量及已开始的项可完成。 |

既有测试适配：

- `TestPostgreSQLCurationExceptionStore`、`TestPostgreSQLCurationExceptionAcceptDecline`、`TestPostgreSQLCurationExceptionTrackAcceptDecline`、`TestPostgreSQLCurationExceptionMergeAcceptDecline` 和艺人双候选测试改用已结清建议，继续验证分页、操作归属、互斥候选、并发采纳、恢复部分写入、陈旧和抑制。已开始采纳同一艺人的另一候选时，后来的采纳返回 409。
- `TestPostgreSQLCurationModelApply` 增加短专辑缺少审查的拒绝断言，补齐审查标识后继续检查写入、幂等、重跑和撤销。`TestPostgreSQLCurationModelRecording` 检查完整单曲免审与已有对应的幂等行为。`TestPostgreSQLCurationModelResolveMatchProgramChain` 用严格选择答复并保留写入、归组和撤销断言。
- `TestPostgreSQLCurationModelReviewUnavailableBackoff` 补全可选编号和真实曲目，继续验证 5 分钟、30 分钟、2 小时及退回原队 24 小时的退避，重审期间始终无写入。`TestPostgreSQL整理审查策略五版发行并入证据` 验证 v5 审查数据、v4 批准失效及缺外部对应时政策结项。
- `TestPostgreSQLArtistWebSearchMockMediaAndUsage`、`TestPostgreSQL整理网页媒体校验与独立审查` 和相关网页测试用搜索结果、真实合成 HTML 及图片下载，保留媒体验证、来源、抑制、身份拦截和用量断言。网页来源开关、原生搜索装配、登录与后台隔离仍经过原入口。
- 原每日额度、模型停止条件、后台名额、三次未处理、同证据搁置、开启者不可用、重启恢复和五档队列用例通过。两个回归用例覆盖第一档禁用旧字段修改工具，其中发行字段用例也验证普通管家对话可以独立审查。
- 单元 `Test后台审查未采用时记录用量和建议` 校验后台的拒绝语义和摘要、主调用、审查用量，`Test整理第一档选择严格限定已发送的候选` 校验缺项、重复项、跨项编号及非法 JSON。审查策略全文和证据字段的两组规范单元断言均通过。

### 验证命令与结果

- `cd backend && mise exec -- go test ./internal/curation/... ./internal/steward/... ./internal/app/...` 通过。
- `cd frontend && mise exec -- npx vitest run src/test/api-contract.test.ts src/features/curation src/app/curation-settings.test.tsx` 通过，6 个文件，875 通过，1 个既有跳过。临时 node_modules 链接已删除。
- 集成测试分轮通过 `ROOMUSIC_TEST_RUN='^TestPostgreSQL(…)$' sg docker -c 'mise exec -- ./scripts/test-integration.sh'` 收窄。上表及既有回归用例在最终相关轮次通过。初轮失败的过期合同断言、合成夹具及替身解析问题已复测。最后用 `ROOMUSIC_UPDATE_API_SAMPLES=1 ROOMUSIC_TEST_RUN='^TestPostgreSQLAPIResponseSamples$' sg docker -c 'mise exec -- ./scripts/test-integration.sh'` 实录 API 样本，64.38 秒通过。以 `ROOMUSIC_TEST_RUN='^TestPostgreSQL(APIResponseSamples|CurationModel(API|APIMatchErrors|Recording))$'` 比对新样本及接口，全部通过。两种度量的完整命令、结果与原始 JSON 见父任务的 after-e 目录。
- 下面三条缺陷复现用例实测失败，保留预期合同断言。它们使用独立 Compose 项目和合成数据。

### 发现的缺陷

| 复现用例 | 实际结果与定位 |
| --- | --- |
| `TestPostgreSQL整理艺人联网无结果后程序队列排空` | 艺人项已记为 resolved/no_result，完整盘点后程序工作者仍未进入等待。艺人待定项读取排除已结清项，使队列投影的 missingItem 判定持续为真，Upsert 又按相同证据禁止重复建项。M 的早期运行在 poor-05 同样卡住，最终两次度量改用受控艺人替身的三次未处理路径。 |
| `TestPostgreSQL整理网页明确区分艺人时不能合并` | 没有外部或库内佐证，网页明确写两名艺人不同。主模型仍提交 same 并引用该句，弱审查批准后产生 1 笔艺人合并。后端确认引文存在并包含双方姓名，但未核实引文是否支持同一身份。 |
| `TestPostgreSQL整理观察建议的改动字段保持数组` | 选择保留原写法时生成 can_accept=false 的 observation，实际 API 返回 preview.changes=null。前端 decoder 要求数组，真实响应会导致这一页解码失败。API 契约样本中的 review=null 由真实可执行的事实建议覆盖。 |

本轮测试和度量只使用当前任务的隔离环境及受控模型端点。任务所建容器、网络和卷已检查清理。

## 三处生产缺陷修复复测

上表三个复现用例现已全部通过。艺人资料的队列投影与实际处理共用同一判断：当前输入摘要已有无结果或未采用结论时，完整盘点可排空；资料或来源配置改变时仍按新摘要处理。网页合并在后端对照摘录与整页，要求能识别明确陈述两个名字是同一身份的完整句子；未确认的表达保留为观察，审查批准不能覆盖此条件。写入事务重验同一份证据。观察建议保存空改动数组，查询旧观察也返回 `preview.changes=[]`。

修复代理通过 `ROOMUSIC_TEST_RUN` 选取上表三个 PostgreSQL 用例，并在 `backend/` 运行 `mise exec -- go test ./internal/curation/... ./internal/steward/...`，两项命令均通过。

测试代理随后移除 M 驱动的艺人三次未处理绕法，保留临时巡检注入。Oracle 与 First candidate 各重跑一次，均结清原有 11 项等待分流和 17 项艺人联网无结果。模型调用分别从 166 降至 98、从 150 降至 82。 [修复前后度量](research/metrics-2026-11-25.md)

`ROOMUSIC_UPDATE_API_SAMPLES=1 ROOMUSIC_TEST_RUN='^TestPostgreSQLAPIResponseSamples$' sg docker -c 'mise exec -- ./scripts/test-integration.sh'` 通过，仅观察建议所在的例外列表样本内容变化。随后按原命令执行前端契约与整理测试，6 个文件通过，875 项通过，1 项既有跳过。契约完整解码新增的 `observation`，其 `preview.changes=[]`、`review=null`、`can_accept=false`。 [例外列表样本](../../../backend/internal/app/testdata/api-samples/curation/exceptions.json)
