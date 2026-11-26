# 识别判断与名字核对实施步骤

从仓库根目录执行下列命令。每步列出可复用的现有用例。实现代理负责产品代码、脚本回包及因旧接口下线必须同步的现有驱动，新增用例和新断言由测试代理补。集成命令每次使用测试脚本自建的一次性数据库。设计稿这一步不运行这些命令。

## 1. 状态和迁移

- 在 `backend/migrations/` 新增下一个连续号的 `NNNN_curation_model_judge.sql` 和 `NNNN_curation_model_judge.down.sql`。先加逐来源候选/选择、否定名字对、有效名字快照的结构，再迁移历史明确否定、清理旧待办和未采用建议。按 design 第六节保留历史和撤销关系。核对 0064 外键的 ON DELETE CASCADE 以及 0091、0092、0093 旧状态约束。用 0101 已建立的总用量。在 `backend/internal/curation/{match_results,pending_items,work_states}.go` 增加新存取，结果与状态只能更新自己的来源与代次。
- 风险在于误删已采纳建议依赖的 pending 行，以及旧后台 plan 被恢复后重复执行。对会话、例外和真实操作逐项核对，仅清旧后台活动。保留能读历史旧枚举的结构。
- 验证 `ROOMUSIC_TEST_RUN='^TestPostgreSQLMigrationRollbackUpgradeDowngradeAndReupgrade$' mise exec -- ./scripts/test-integration.sh`。这是迁移执行器的回退验证，业务数据保全另由末节的升级用例覆盖。

## 2. 调整三类入口和 B2 输入

- 改 `backend/internal/catalog/curation_evidence.go`、`backend/internal/curation/program/{worker,match,match_search,match_lookup,match_snapshot,release_route,release_state,pass,queue_query,queue_projection}.go`。把实际查询步骤、参数、候选、同名艺人和冲突连同原本地原文交给待办。两种可交模型的逐来源终态走 select，零候选同样可领。版本升为 106。8 的目录门槛在第一次 S8 前生效，知识线索仅在 S8 后处理。使用 8 的本地证据投影中已有的字段，保留 6 的文件代次和步骤检查。
- 改 `backend/internal/curation/{model_queue,model_targets,pending_items,progress}.go` 和 `backend/internal/curation/model/{queue,item,status,scheduler}.go`，将符合条件的发行集合用于取项、计数、写前复核及合并目标。保留 7 的按真实同类内容准备、预算裁候选、事务领取及互斥。接入 8 的 local 分支和 10 的独立队列交接。
- 风险在于把缺少可查材料、请求失败或已撤销候选错误地投影成可领取项，以及目录成员隐藏在 evidence 中漏查资格。
- 验证 `(cd backend && mise exec -- go test ./internal/curation/program -run '^(TestProgramReleaseAdmission|TestProgramNameSearchOrder|TestProgramIdentifierSearchOrder|TestProgramRuleUpgradeStatePolicy)$')`。运行 `ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationModelRules(DirectoryAndPriority|TargetMutualExclusion|TrimCandidates)$' mise exec -- ./scripts/test-integration.sh`。

## 3. 接一次回复、分批与逐项落盘

- 改 `backend/internal/app/{app,curation_model}.go`、`backend/internal/steward/httpapi/background.go`、`backend/internal/steward/runtime/{run,run_types}.go`、`backend/internal/curation/{model_api,model_run}.go`、`backend/internal/curation/model/{item,scheduler}.go` 和 `backend/internal/curation/stewardtools/tools.go`。接入设计中的 B2 JSON，以及 B3 分类回包。复用 Selection 的单步文本与后台动作记录，估算/实发同步，校验实际发送的候选、艺人和独立的每行答案。服务器动作直接把已解析 B2 答案和断点交程序；真正定不了调用 FinishUndetermined。把来源等待与模型项的空答、上游故障分别结算。
- 风险在于批内一个坏答案触发已完成项再问、模型意外输出的工具调用进入执行器，或崩溃后丢掉已经落盘的答案。只有由解析器从完整文本生成的、与当前领取相符的后台动作可提交。
- 验证 `(cd backend && mise exec -- go test ./internal/app ./internal/curation/model ./internal/steward/runtime -run '^(TestCurationModelRulesUndeterminedRequiresReason|TestCurationModelRulesBatchBudgetBoundary|TestCurationModelRulesStepRetries)$')`。运行 `ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationModelRules(PartialReply|Batch400|BatchPacking|LongInputReopens)$' mise exec -- ./scripts/test-integration.sh`。

## 4. 执行线索、结构核对和恢复

- 在 `backend/internal/curation/program/model_hints.go` 实施五类线索及有序组。改同目录 `model_matcher.go`、`model_editions.go`、`model_verification.go`、`match_search.go`、`match_lookup.go`、`match_snapshot.go`、`model_queue.go`、`pass.go`、`release_route.go`、`release_state.go`、`queue_projection.go`、`queue_query.go`、`queue_recovery.go`、`queue_progress.go`。去除旧“任选一版再折到组”与“结构不够再审查”的 B2 路径，复用 A2.3 和实际写入计划。按已定短专辑条件，仅在 B2 有相应独立佐证时接入 `match_album.go` 的判据。
- 改 `backend/internal/sources/{request,queue}.go`，从实际 MusicBrainz 发送前扣本张的持久化余量，短重试和详情也计。沿用 6 的缓存和来源等待。停在当前具体步骤，成功变更代次后更新逐来源结果及工作行。多张通过但无法区分时保存通过集合供人挑，与普通跨组/硬编号冲突分别记原因。补 `ambiguous_editions` 的 90 天程序重查。
- 风险在于请求额度只数搜索、查到第一张即误认唯一、缓存恢复时重置额度，或核对后的写入再次无上限展开组内所有版本。查看真实 mock 出站数，并对写入已提交一部分后重放使用同一幂等键。
- 验证 `(cd backend && mise exec -- go test ./internal/curation/program ./internal/sources -run '^(TestAlbumContraryEvidence|TestProgramPartialAlbumNamesOnlineAndReplay|TestProgramAlbumGroupSummary|TestQueueHTTPShortRetrySucceeds|TestQueueHTTPBackoffAndRecovery|TestProgramUnmatchedRecheckSchedule)$')`。运行 `ROOMUSIC_TEST_RUN='^(TestPostgreSQLProgramMatchFailedLookupKeepsLastSuccess|TestPostgreSQLProgramModelMatchSameKeyKeepsNewerConclusion|TestPostgreSQLProgramPeriodicRecheckCallsAcoustID)$' mise exec -- ./scripts/test-integration.sh`。

## 5. 名字和身份核对

- 改 `backend/internal/curation/program/{name_verification,model_verification,artist_links}.go`、`backend/internal/curation/{verification,artist_credit_proposals,artist_merge,model_targets}.go`、`backend/internal/curation/stewardtools/verification.go`。维护五类名字比较，保存有来源的名称快照，只有同文字一两个字可能有误才排队。错字使用已生效对应的外部值与模型外部依据级别，执行前查保护、撤销与修订，保留/拿不准直接结项。身份维持程序确定路径，其余带别名、备注、国家、角色、依据操作入队。same 复用对应、合并操作；different 按无序名字对在投影、物化、领取、写入四处一致处理。已有 release_merge 单独按原核实与治理入口直接提交或结案。
- 风险在于把修正显示文字当成合并身份、人工改名后仍提交旧值，或批处理的两份发行同时询问相同的艺人对。名字队列单列 pending，专辑 matched 时就计完成。
- 验证 `ROOMUSIC_TEST_RUN='^(TestPostgreSQLCurationArtistLinks|TestPostgreSQLCurationAutoMergeRevertSuppresses|TestPostgreSQLProgramSplitArtistCreditLifecycle|TestPostgreSQLProgramMergeSameEdition|TestPostgreSQL整理名字核对按新依据重排一次)$' mise exec -- ./scripts/test-integration.sh`。

## 6. 交付人选、名称采用与来源接口

- 改 `backend/internal/curation/{httpapi/handlers,httpapi/match_selection,match_results}.go`、`backend/internal/curation/program/model_matcher.go`、`backend/internal/catalog/{curation_match_names,projection,artists_types,artists_reads,artists_media,httpapi/handlers}.go`、`backend/internal/app/{app,api_samples_integration_test}.go` 及对应的 `backend/internal/app/testdata/api-samples/` 样例。按设计第五节实现分页列表、逐张候选、选择结果、MB 写法及采用、艺人判断来源。窄接口由 app 装配；沿用原有管理员、同源、修订、操作幂等与撤销。与主代理核对响应样本后由其接前端。
- 风险在于 POST 跳过结构重验、GET 展示另一代或已撤销的来源、重复点选产生第二次写入。旧库已匹配的名称在读取时按仍有效的依据补齐，不触发重新搜索。
- 验证 `ROOMUSIC_TEST_RUN='^(TestPostgreSQLProgramMatchResultsAPI|TestPostgreSQLCurationExternalLinkArtistRevertSuppresses)$' mise exec -- ./scripts/test-integration.sh`。新 API 的响应、权限和并发用例由测试代理加入。

## 7. 删除后台旧入口

- 改 `backend/internal/curation/{patrol,patrol_submit,local_submit,model_exceptions,fact_suggestions,work_states,model_api,pending_items,release_merge,release_merge_discovery,suppressions}.go`、`program/{worker,model_queue,release_state,queue_query,pass,artist_media_pass}.go`、`model/{queue,item,scheduler,status,exceptions}.go`、`stewardtools/{patrol,tools,release_merge,verification}.go` 及 `httpapi/handlers.go`（后三组在 `backend/internal/curation/`）。去掉巡检规范名的全部生产消费、PatrolPromptVersion、LookupTierReady、查库/联网档、未采用建议和待审查退避。断开艺人资料/身份的旧自动联网流。保留 8 使用的本地提交以及 10 使用的独立服务。在 `backend/internal/app/{app,curation_patrol_prompt,curation_model}.go` 取消后台工具、网页权限及整理审查适配，去掉旧示例中的巡检字段与异常列表。更新真正依赖旧合同的已有测试驱动，如 `curation_model_{fallback,review,selection,exception}_*`、`curation_patrol_integration_test.go`、旧 metrics 模型驱动；保留验证原能力的互动审查用例。实现代理不新增测试文件。
- 风险在于仅删除 UI 状态却仍建会话、模型可通过原生搜索联网、清理公共注册表时伤及互动对话，或仍有旧 SQL 在恢复时取出审查计划。检查后台单独装配及历史解码。
- 验证 `(cd backend && mise exec -- go test ./internal/steward/runtime ./internal/curation/model -run '^(TestC7ReviewRejectsInvalidResults|TestC7ReviewCallStrictStreamAndDeadline|TestCurationModelRulesReadyKindOrder)$')`。执行 `rg -n 'PatrolPromptVersion|LookupTierReady|ReviewBackoff|submit_patrol' backend/internal/{app,curation} -g '*.go' -g '!**/*_test.go'`，产品运行代码预期无命中。管家设置/互动审查和 10 的独立艺人功能不计入整理零调用。

## 8. 固定样本、规范与合入门禁

- 改 `backend/internal/upstreammock/script.go` 和 `backend/internal/app/testdata/curation_regression/fixed-script.json`。脚本按本次请求的 kind 与**每一项**证据匹配，再把实发 item_id 填入 JSON 数组；沿用现有规则优先次序，兼容 8 单目录工具脚本。不以整批首个命中覆盖其他项，不从评分真值库读取 MBID。one-char 发名字线索“枝航谱”，B3 再回 typo；romanized 发原文“星の窓”，结构核对之后保留本地罗马字。合成源真实收到新的查询词，读详情后写入 `fixed.json` 指定版本和对应录音。删掉旧查库/升级/联网/审查回包。embedded-artist 与旧编码的 B1 规则归 8。更新既有报告驱动对旧调用路径的引用，保留 `fixed-baseline.json` 作为只读参照。
- 测试代理在 `backend/internal/app/curation_fixed_assertions_integration_test.go` 将 one-char 断言扩展到实际 B2 查询参数、调用次数和结构证据，补 romanized 的版本与本地标题断言。固定库对两种模式都评分。先跑定向门禁，修定向错误后跑完整固定库。运行整套后保存真实差值再交付。
- 同步 design 第七节列出的整理合同、运行时、独立审查、艺人来源、数据库和固定样本规范；完整整理合同改写归 11。
- 验证命令如下。基线有 28 张，纯程序认对 12 张、错 0、72 次、70401 ms，脚本认对 15 张、错 0、343 次、125876 ms。以此次固定报告逐模式列认对张数、认错、请求总数及每张均值、总用时及与基线的差值。既有指标门禁可能只报告耗时回退，交付时须人工对照，不能将命令成功等同于验收通过。

```sh
(cd backend && mise exec -- go test ./internal/upstreammock ./internal/app -run '^(TestScriptCurrentItem|TestFixedSampleInventory|TestPackageDependencyDirections)$')
ROOMUSIC_CURATION_METRICS_SAMPLES=one-char,romanized ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationFixedSelectedReport$' mise exec -- ./scripts/test-integration.sh
ROOMUSIC_CURATION_METRICS_SUITE=fixed ROOMUSIC_CURATION_METRICS_OUT=../../../temp/curation-model-judge-fixed.json ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationMetrics$' mise exec -- ./scripts/test-integration.sh
```

## 测试代理补充清单

| 用例 | 正例与对照的可观察结果 |
| --- | --- |
| B2 请求与先后 | B1 所需目录先辅助；错字和罗马字经过普通 S8 再进 B2；没有模型、节目类型、缺准入、等待和已认组者的 B2 取项与计数均为零。重扫变为有声书后旧领取无法落库。 |
| 批内答案与超长 | 同类多行中，一张先选再列组，一张无候选只给组，一张明确无解，一张格式错误。断言实发候选白名单、成功行不重问、漏答独立重试、批量 400 拆单、单项裁候选及保留完整本地资料。 |
| 五类线索 | 年份、地区、版本、介质只在同组区分；同名艺人只能选提示中过的 MBID；散曲按录音及时长，古典按作品与作曲者；合辑按群星，非音乐停止。注释内目录号、文件名条码能查，同形字符/大小写变化、跨字段、只在 MB 候选或模型记忆中的编号零查询。 |
| 六次和错误 | 先选成功则后组零请求；较早组全不符才到下一组。第六次正好完成、仍缺详情、分页不完整、缓存命中、429 短重试、来源等待/恢复、无效响应三次、服务重启分别统计真实 HTTP 尝试，预算不能重置。两个批内项的预算互不借用，来源失败不记未匹配。 |
| 冲突/人选 | S8 同组直接认组的对照。B2 同组/跨组有多版均合格时停止并列全已核实者；无模型的 S9 也能展示。允许选择者通过 A2.3 写一版；被占用/撤销、文件变化、错位、曲长反证、并发不同选择、重复同键分别检查状态、返回与操作数。定不了与 90 天重查均先回程序。 |
| B3 名字与接口 | 五种情况分别覆盖专辑名和曲名，另测署名与名字差很多的两首歌。只有近似名发模型请求；typo 自动可撤销；keep 或拿不准无建议；手改后晚到的模型答案不覆盖。已匹配时阻塞 B3，专辑仍完成。普通成员能读 MB 名称、管理员能一键采用，核对依据过期报 409。 |
| 艺人身份 | 名字/别名、角色对调程序直接处理。same 建对应/合并各测一例，撤销后来源标志消失；different 在另张发行、反向名字顺序、服务重启和规则升级后无第二次询问，并发同对只领一次；undetermined 不产生全局否定。 |
| 清理和作用域 | 隔离库植入活动巡检、联网、lookup、待重审、open/accepted 建议、已写字段/操作、旧明确结论、聊天计划。升级、启动恢复、降级和再升，验证各项保全及恢复行为。真实后台会话对旧工具、模型原生搜索、网页端点、reviewer、重审五分钟唤醒计数均为零，互动对话仍可审查。 |
| 固定验收 | 对 one-char、romanized 读取真实请求序列，核对纯程序与脚本两轮、限定的一次有效识别回答、指定发行与录音、名字保留/纠正、零错写。补同艺人但逐轨名字或时长错误的短专辑反例。执行上方依赖门禁和两个固定命令，保留实际 JSON 与差值。 |

## 实施报告

识别规则由 105 升至 106。S8 的普通短专辑和部分吻合继续使用原判据。B2 的短专辑名字线索须命中查询标题，且唯一候选的本地艺人、全碟全轨位置、真实曲名、时长逐一通过，才能补足专辑标题差异。A2.3、ADR 0009 和设计稿均已同步。模型提交一次识别回答，之后由程序先核对所选候选，再逐组查证，每张最多额外出站六次。程序将组下标、已用次数与当前代次持久化，组内重放使用来源缓存。多个合格但无法区分的版本进入人选。B3 独立结项，已判不同的艺人按无序名字对记忆。

### 固定样本与验证

28 张固定样本按全库作分母。括号内是相对既有固定基线的变化。时间为本次实际耗时。

| 模式 | 认对 | 认错 | 请求数 | 每张请求 | 总毫秒 |
| --- | ---: | ---: | ---: | ---: | ---: |
| 纯程序 | 16（+4） | 0（0） | 65（−7） | 2.32（−0.25） | 61664（−8737） |
| 程序加脚本模型 | 18（+3） | 0（0） | 87（−256） | 3.11（−9.14） | 73471（−52405） |

- one-char 通过“枝航谱”查询命中指定发行及 4 个录音，并由独立名字核对修正本地标题。定向运行实际共 8 次请求，其中 B2、B3 各调用一次模型。
- romanized 通过“星の窓”查询命中指定发行及 4 个录音，保留本地罗马字标题。定向运行实际共 7 次请求，含 1 次模型识别。两张单跑纯程序时各有 5 次请求，均未收录。
- 第 1 至 8 步列出的五组单元验证、八组 PostgreSQL 定向验证和固定库均已通过。旧入口符号的产品代码检索无命中。另在 backend 下执行 `mise exec -- go test ./internal/app ./internal/catalog ./internal/catalog/governance ./internal/catalog/httpapi ./internal/curation ./internal/curation/httpapi ./internal/curation/model ./internal/curation/program ./internal/curation/stewardtools ./internal/sources ./internal/sources/musicbrainz ./internal/steward/httpapi ./internal/steward/runtime ./internal/upstreammock`，命令成功，其中 curation/httpapi 没有包内单元用例。
- 数据来自临时独立测试库和脚本上游。完整指标在 `temp/curation-model-judge-fixed.json`，原始日志在 `temp/judge-confirm1.log` 至 `temp/judge-confirm8.log` 和 `temp/judge-final-units.log`。本次按限定范围执行，未运行全库 PostgreSQL 用例和 REST 全量录制器。

### 冲突接口与合入

| 操作 | 输入及界面可用的结果 |
| --- | --- |
| 管理员冲突列表 | `GET /api/v1/admin/curation/conflicts?page=1&page_size=20` 返回 `items` 及 `pagination{page,page_size,total}`。每行含 `release_id,title,source,status,checked_at,reason,conflicts,snapshot_id,candidates,selection`。 |
| 单张的逐来源结论 | `GET /api/v1/admin/curation/releases/{id}/match-results` 返回 `results[]`。候选描述包括 `external_id,entity,title,artist,release_group_id,date,country,track_count` 和可用的 `medium,verification,conflicts`。以响应内的快照和候选 ID 提交。无查询时返回空行集。 |
| 选择候选 | 管理员同源 `POST /api/v1/admin/curation/releases/{id}/match-selection`。请求头 `Idempotency-Key` 为 UUID，请求体含 `source:"musicbrainz",snapshot_id,external_id`。200 返回已匹配的来源行，其 `selection` 含 `external_id,selected_by,selected_at,operation_ids`。同键同用户同输入可重放。陈旧或已替换的快照返回 409 `item_stale`，重验结构不符返回 409 `insufficient_corroboration`，治理阻止写入时返回对应的冲突码。来源不可用返回 503 `source_unavailable`。请求参数或 JSON 无效返回 400。 |
| 名字与依据 | 专辑详情 `musicbrainz_names[]` 提供对应写法，管理员按依据采用。艺人对应的 `judged_by_model` 标明目前生效的模型依据。 |

迁移以 `backend/migrations/NNNN_curation_model_judge.sql` 与对应的 `.down.sql` 交付。增加逐来源候选快照及挑选结果、有效 MB 名字和艺人否定对，结束旧待办并清理未采用建议。已写入曲库的操作及已采用建议保留。独立验证时，当前工作树的上一号为 0103，临时占用 0104 验证升级、回退和再升级。子任务 8、10 合入后由主代理分配下一个连续号，再用于部署。与 8 共用的原巡检、本地辅助和目录领取入口，及 10 的独立艺人队列，按设计在合入时对接。

已删除八份旧建议、重巡及其错误样本，并更新现有 API 录制驱动。任务限定的命令未包含 `TestPostgreSQLAPIResponseSamples`。冲突列表、详情和进度等静态 REST 样本的重录、前端解码和页面接线留给合入阶段。新增反例和并发/恢复场景见上方测试清单。

### 已有断言与驱动的调整

以下文件名以 `backend/internal/app/` 为基准，另有一项注明目录。未新增测试文件或用例函数。

| 既有文件 | 调整 |
| --- | --- |
| curation_authenticity_integration_test.go | 去掉巡检版本字段，保留扫描原文、真实性与指纹覆盖。 |
| curation_patrol_integration_test.go | 移除增量巡检、重巡和使用巡检规范名搜索并合并的旧用例。目录辅助仍有原用例。 |
| backend/internal/curation/program/queue_priority_integration_test.go | 移除旧第二档与巡检就绪的断言，保留程序识别优先。 |
| curation_match_c_integration_test.go | 无候选在模型开启时为 select/waiting_model，去掉旧巡检及第二档就绪判断。 |
| curation_model_selection_test.go | 把原选版、升级改为 judge/undetermined 和服务器 submit_judge/shelve，继续检查跨项引用、重复、漏答和无理由的结案。 |
| curation_model_scheduler_integration_test.go | 优先级、并发、同类领取与互斥改用有效本地项或有来源的近名项。脚本回包按实发项 ID 生成。 |
| curation_program_contract_integration_test.go | 逐来源结果带空候选。90 天重新走 S8 后可交 select，手动复查只走程序。原同键用例使用人选服务真实落库，核对同键重放、后续新快照 409 及治理操作数。 |
| curation_live_gaps_match_integration_test.go | 删除旧档位入参，按一次 JSON 含当前证据解释识别。 |
| curation_live_gaps_queue_integration_test.go、curation_model_fallback_integration_test.go | 合成客户端沿用准备阶段的限速上下文，改用 PreparationContext。 |
| curation_name_retry_integration_test.go | 近名 fixture 绑定有效发行与录音对应，按单次 typo 答复修正。继续核对输入变更后的原文、写入来源和撤销。 |
| curation_release_merge_model_integration_test.go | 后台工具白名单改为保留的五项。合并以一次 JSON 结案，来源为 model，去掉整理审查 ID 预期。样本直接以真实治理抑制服务建立。 |
| curation_live_usage_integration_test.go | 历史用量 fixture 明示原 route，脱离旧 prompt 生成。 |
| curation_metrics_model_integration_test.go、curation_metrics_integration_test.go | 历史录制 DTO/字符串仅在 fixture 中还原，队列排空去掉巡检条件。 |
| curation_fixed_assertions_integration_test.go | 扩充既有错字验收到罗马字样本，按实际 MB 查询、详情、匹配和标题结果评分。只有这两个指定名字线索样本要求调用模型。 |
| api_samples_integration_test.go、curation_samples_integration_test.go | 固定录制流程接入冲突列表，移除已下线建议/重巡请求，保留后台会话权限及实际操作样本。 |

### 实际改动文件

- `.trellis/spec/backend/`　`artist-contracts.md`、`curation-contracts.md`、`database-guidelines.md`、`quality-guidelines.md`、`steward-review-contracts.md`、`steward-runtime-contracts.md`
- `.trellis/tasks/11-26-curation-model-judge/`　`design.md`、`implement.md`
- `backend/internal/app/`　`api_samples_integration_test.go`、`app.go`、`curation_authenticity_integration_test.go`、`curation_fixed_assertions_integration_test.go`、`curation_live_gaps_match_integration_test.go`、`curation_live_gaps_queue_integration_test.go`、`curation_live_usage_integration_test.go`、`curation_match_c_integration_test.go`、`curation_metrics_integration_test.go`、`curation_metrics_model_integration_test.go`、`curation_model.go`、`curation_model_fallback_integration_test.go`、`curation_model_scheduler_integration_test.go`、`curation_model_selection_test.go`、`curation_name_retry_integration_test.go`、`curation_patrol_integration_test.go`、`curation_patrol_prompt.go`、`curation_program_contract_integration_test.go`、`curation_release_merge_model_integration_test.go`、`curation_samples_integration_test.go`、`steward.go`
- `backend/internal/app/testdata/api-samples/curation/`　`exception-accepted.json`、`exception-declined.json`、`exceptions-merge.json`、`exceptions-track.json`、`exceptions.json`、`repatrol.json`
- `backend/internal/app/testdata/api-samples/errors/`　`exception-decided.json`、`exception-stale.json`
- `backend/internal/app/testdata/curation_regression/`　`fixed-script.json`
- `backend/internal/catalog/`　`artists_media.go`、`artists_types.go`、`curation_evidence.go`、`curation_match_names.go`、`projection.go`
- `backend/internal/catalog/governance/`　`merged_sources.go`
- `backend/internal/catalog/httpapi/`　`handlers.go`
- `backend/internal/curation/`　`adopt_name.go`、`artist_credit_proposals.go`、`external_link.go`、`local_submit.go`、`match_results.go`、`model_judge.go`、`model_queue.go`、`model_targets.go`、`name_difference.go`、`patrol.go`、`patrol_submit.go`、`pending_items.go`、`progress.go`、`verification.go`、`work_states.go`
- `backend/internal/curation/httpapi/`　`handlers.go`
- `backend/internal/curation/model/`　`item.go`、`queue.go`、`scheduler.go`、`status.go`
- `backend/internal/curation/program/`　`artist_links.go`、`artist_media_pass.go`、`enrich.go`、`match.go`、`match_album.go`、`match_lookup.go`、`match_search.go`、`match_selection.go`、`model_hints.go`、`model_matcher.go`、`model_queue.go`、`model_verification.go`、`name_verification.go`、`pass.go`、`queue_artists.go`、`queue_priority_integration_test.go`、`queue_projection.go`、`queue_query.go`、`release_route.go`、`unit.go`、`worker.go`
- `backend/internal/curation/stewardtools/`　`patrol.go`、`release_merge.go`、`tools.go`、`verification.go`
- `backend/internal/sources/`　`request.go`、`sources.go`
- `backend/internal/sources/musicbrainz/`　`entity.go`、`project.go`
- `backend/internal/steward/httpapi/`　`background.go`
- `backend/internal/steward/runtime/`　`run.go`
- `backend/internal/upstreammock/`　`script.go`
- `backend/migrations/`　`NNNN_curation_model_judge.down.sql`、`NNNN_curation_model_judge.sql`
- `docs/architecture/adr/`　`0009-curation-chain-review.md`
