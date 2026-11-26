# 实施顺序和定向验收

本文件供实现代理、测试代理和负责设置页的主代理交接。每步从仓库根目录执行命令；Go 单元测试在 `backend` 模块中用 mise 运行。下面是实施时的验证计划。当前设计阶段只做文档核对。

## 1. 迁移、统一日账和通用设置

- 新建 `backend/migrations/NNNN_curation_model_rules.sql` 和同号 `.down.sql`，编号取父任务合入位置。按设计 §5 建单行模型状态、项级标记和 `max_request_tokens`，把旧 web 子额度/统计列原位更名，只作降级快照；移除标记为七天等待的旧项的时间限制。旧总账直接沿用。down 恢复列名和旧约束，移除新状态。
- 改 `backend/internal/curation/settings.go`、`model_usage.go`、`model_queue.go`、`model_api.go`、`httpapi/handlers.go` 及 `model/status.go`、`model/queue.go`、`model/scheduler.go` 的 web 日限读写及调用点。用 `TodayUsage` 和 `NextBudgetDay` 形成整理/独立艺人可共用的日额判定。保留按日 UPSERT 与当前项实耗同事务，移除 web 分账参数。每日限额、大小设置与模型修改成功后唤醒。进度返回当天累计、限额和恢复时间。
- 风险是把 web 子项又加一遍，或把现有 null 不限改回默认。旧 Go 测试夹具和 API 样本仍引用 web 字段，和测试代理同步替换受影响的引用，避免仅编译测试二进制就失败；业务实现代理不写新测试。主代理同时更新严格解码旧字段的前端才可发布。
- 验证嵌入的上下行 SQL、迁移往返及日账。第二条只选本次版本的子用例。

```sh
(cd backend && mise exec -- go test ./internal/platform/postgres -run '^TestEmbeddedMigrationRollbackFiles$')
ROOMUSIC_TEST_RUN='^TestPostgreSQLMigrationRollbackRoundTrip$/^[0-9]+_curation_model_rules[.]sql$' mise exec -- ./scripts/test-integration.sh
ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationModel(DailyLimit|ConcurrentDailyLimits)$' mise exec -- ./scripts/test-integration.sh
```

## 2. 在真实请求边界分类并传回调度层

- 改 `backend/internal/steward/provider/errors.go`、`client.go`、`backend/internal/steward/runtime/run.go`、`run_types.go`、`backend/internal/steward/httpapi/background.go`、`backend/internal/app/curation_model.go`、`backend/internal/curation/model/scheduler.go`。沿用 `provider.Error` 与 `runtime.RunEnd`，保留 kind、HTTP 状态及脱敏原因，补稳定上游码和 Retry-After/限额重置时间。应用层将结果交给 `Run.Wait`。新结构的适配器、launcher 实现、已有测试替身在同一改动中对齐。
- 按设计 §3 先识别明确的配置与余额错误，再执行临时故障的短重试。在后台 `readModel` 整步重试，复用现有次数、等待和 10 秒预算；后台发一次底层 HTTP 的方法复用适配器组包、出站及解析。每次失败片段单独丢弃、usage 单独上报。被取消的步骤不再发。交互调用与旧重审路径继续使用其现有入口。
- 风险是同时触发两层最多 5 次的重试，或者先重试 429 再读出余额不足。流中途断开必须重建同一请求的解析状态，不能重新执行已完成的上一轮工具。
- 验证三个协议的已知错误、密钥脱敏、上游中断与普通运行生命周期。

```sh
(cd backend && mise exec -- go test ./internal/steward/provider -run '^(TestProtocolsHTTPAndStreamErrorsRedactKey|TestModelRetries503AndCompletesStream|TestUpstreamMockDropNeverCompletesTurn)$')
(cd backend && mise exec -- go test ./internal/steward/runtime -run '^(TestRunTruncatedProviderStreamAndOutputLimit|TestRunLimitsCancellationAndProviderFailuresReleaseConversation)$')
```

## 3. 落实五类处置、暂停恢复和设置页重试

- 在 `backend/internal/curation/model_failures.go`、`pending_items.go`、`work_states.go`、`model_api.go` 实现逐项结算及全局暂停。改 `model/item.go`、`model/scheduler.go`、`model/status.go` 的通用出口，使用配置代次、领取、输入摘要及行锁。定不了和有效答案只在项成功提交时计一次；空/漏答项单独安排重试，单项拒绝或三次空答搁置，连续十个不同项的同类故障按设计暂停。更新 `UpsertPendingItem` 等受影响的重新入队条件，并在换模型、文件变化或调高大小上限时清对应失败标记。旧重审退避分支留给 9。
- 在 `item.go` 删除用尽档位额度后 `+7天` 的等待；运行中和启动恢复共用同一来源恢复逻辑。旧档位的到期领取仍按原逻辑复位项内计数。
- 改 `backend/internal/steward/provider/probe.go`、`backend/internal/steward/httpapi/background.go`、`backend/internal/steward/httpapi/settings.go`、`backend/internal/app/curation_model.go` 和 `backend/internal/curation/httpapi/handlers.go`。补有效配置标识与后台小探测；增加管理员 `POST /model/retry` 及设计 §6 的进度字段。上游欠费在到时的调度循环中单独探测，手动测试与自动探测复用同一运行名额/互斥。通过应用层连接保存设置后的唤醒。未配置、已欠费、暂时冷却、本地日限各有自己的状态。
- 风险是已在途的旧配置结果重新锁住新模型，或剩余的配置暂停被并发成功清掉。旧 `model_updated_at` 同时受额度/并发保存影响，不可用它判定换了模型。探测失败仍会有安全原因，工具探测的 `unconfirmed` 不可视作手动恢复成功。
- 验证现有配置可用性、待办生命周期及长于请求内预算的 Retry-After；已改变的预期交测试代理更新。

```sh
(cd backend && mise exec -- go test ./internal/steward/provider -run '^TestProbeProtocolsModelsAndThreeToolConclusions$')
ROOMUSIC_TEST_RUN='^(TestPostgreSQLStewardBackgroundAvailabilityAndStart|TestPostgreSQLCurationPendingLifecycle|TestPostgreSQLCurationModelProviderRetryAfterBeyondBudget|TestPostgreSQLCurationModelUnconfiguredEnabler)$' mise exec -- ./scripts/test-integration.sh
```

## 4. 把就绪判断移到建会话前

- 改 `backend/internal/curation/model/queue.go`、`model/status.go`、`model/scheduler.go`、`model/item.go`（相对 `backend/internal/curation`），以及 `backend/internal/curation/model_queue.go`、`pending_items.go`。扩展已有预选/准备/领取路径，复用目标互斥、目录分组和来源队列的共享时钟。传入本项实际所需来源；需要来源且正在暂停的项只更新可领取时间。来源就绪且配置、额度均允许后才建会话，认领时再对 `updated_at` 和摘要。
- 在 SQL 的 kind 映射和 Go 的次序同时接入本地辅助、识别判断、名字/艺人核对。同一标签下仍须满足相同发送/回答合同。8、9 可直接提供其各类输入、所需来源、有效答案或定不了的逐项结果。旧档位和巡检只走原执行器，不在这一步删除其代码。
- 风险是队首一个需 MusicBrainz 的项让本地项也停住；另一个是先创建再删除空会话，表面上会话表为零但实际发起次数大于零。测试用 launcher 计数并核实上游请求数。
- 验证暂停只推迟缺明细的项、目标互斥与正常并发。

```sh
ROOMUSIC_TEST_RUN='^(TestPostgreSQL整理第一档连续限流只推迟缺详情的项|TestPostgreSQLCurationPendingClaim|TestPostgreSQLCurationModelConcurrency)$' mise exec -- ./scripts/test-integration.sh
```

## 5. 完整请求估算、凑批与普通 400 拆单

- 改 `backend/internal/steward/provider/client.go` 的现有协议编码路径、`backend/internal/steward/runtime/run.go`、`run_types.go`、`backend/internal/steward/httpapi/background.go`、`backend/internal/app/curation_model.go`。使后台预估与实际请求共享最终系统词、实际工具、消息编码和输出预留；实际出站前核对同一预算。调度侧通过 launcher 使用估算，不导入 provider。
- 改 `backend/internal/curation/model/item.go`、`queue.go`、`scheduler.go`、`backend/internal/curation/pending_items.go`。用设计 §4 算法替换 `selectionMessageBytes/selectionBatchSize`，本地辅助按完整目录取，判断、名字核对按同类和长度装入。候选裁剪只改已发送的副本，`selectionActions` 或 9 的解析器跟随副本校验。单项仍超长先记录再返回，零调用。普通批量 400 在事务中给所有尚属该批的项标记单发，唤醒后用新会话单项发送。
- 风险是估算时遗漏工具 schema、对预留了 1/8 的请求实际却仍发固定 8192 输出，或复用前一批历史使拆单仍然超长。跨项名字核对需让 9 的准备函数给出每项各自的本地证据，目前 `taskMessage` 的单条 proof 不能复制给全批。
- 验证实际协议请求的工具和输出边界，及“不可选未发送候选”的已有样本。

```sh
(cd backend && mise exec -- go test ./internal/steward/provider -run '^(TestProtocolsTextUsageAndRequestPrivacy|TestC8ResponsesRequestToolsResultsAndForcedReview)$')
(cd backend && mise exec -- go test ./internal/app -run '^Test整理第一档选择严格限定已发送的候选$')
```

## 6. 同步规范、验收与合入

- 对照 ADR 0009 §6.1 更新 `.trellis/spec/backend/curation-contracts.md` 中的调度、计次、恢复、字段和额度段落，更新 `.trellis/spec/backend/steward-runtime-contracts.md` 的后台错误、usage 与短重试段落；在 `.trellis/spec/backend/steward-config-contracts.md` 记录已保存配置与整理测试入口的关系。给主代理交付设计 §6 的字段/返回值和旧前端字段删除清单；给 8、9、10 交付逐项结果、依赖来源和共享用量入口。合入前核对迁移号、聚焦测试的实际用例数和下列门禁。
- 风险是把仅后台运行适用的规则写到管家对话，或为了跨包返回 provider 类型而放宽依赖白名单。应用层承担两套类型的映射。

```sh
git diff --check
(cd backend && mise exec -- go test ./internal/app -run '^TestPackageDependencyDirections$')
```

## 测试代理补充清单

实现代理负责业务代码，测试代理负责下列新增、调整的合成样本及旧样本的替换。可以复用 `backend/internal/upstreammock/scenario.go` 的限流、不可用、断线场景及 `script.go` 的文本/工具答复；欠费、带重置头、半个 SSE 后断开等缺失样本由测试侧 HTTP 夹具补齐。与子任务 1 共用已有 mock，测试不接真实端点。

| 样本和位置 | 要断言的结果 |
| --- | --- |
| `backend/internal/steward/provider`、`runtime` 的协议错误和步级重试 | 401/403、普通 404 对模型不存在、400 工具拒绝对普通 400、402/400/429 明确余额对普通 429、500/502/503/504/529、连接失败/超时/流中断、格式错及 200 中的错误事件；错误原因脱敏。短重试首发在内最多 5 发、10 秒预算不重置，成功只落一次工具，失败尝试的已知 usage 各计一次，取消直接结束 |
| `backend/internal/app/curation_model_stop_integration_test.go`、`curation_model_scheduler_integration_test.go` | 单项拒绝不阻塞其他项；9 项与第 10 项相同拒绝、不同原因和有效答案打断；空返回同一项第 1、2、3 次、十个不同项首次空答；同项重复不凑十项；真正定不了带理由且只计一次；部分批有效只重来缺答。进程恢复、配置更改和并发收尾后计数准确 |
| 同一调度夹具及 `curation_model_decision_integration_test.go` | 401 停到配置改变或测试成功，切换继承模型也能恢复；原设置重存、调并发不能解锁。402 第 59 分钟无请求，第 60 分钟恰好一个，小探测成功/失败、管理员手动探测与定时重叠；本地日限无欠费探测。临时退避 5、10、20…180 秒和成功清零、有效/失效 Retry-After、重置时间，以及已暂停时仍有旧在途结果 |
| `backend/internal/app/curation_selection_backoff_integration_test.go` 及调度夹具 | 来源暂停时 launcher 建会话调用数和模型请求数都为 0；纯本地或资料齐备项照跑；恢复后原项被领取。未配置时三类零会话，程序结论正常；三个就绪种类的分派顺序、同目标互斥、并发上限、生效模型一致 |
| `backend/internal/curation/model`、`backend/internal/app/curation_model_selection_test.go` 及调度夹具 | 合成中日韩文、ASCII、JSON 转义和工具 schema；等于预算可发、多 1 则拆。较长项留到下批、同类多项、不同合同分包、一个目录超过预选窗口也取全。超限先裁最低分候选，同分保序，本地信息完整；仍超限记“内容太长”且零请求，调高后可重做。多项普通 400 发一次批请求后各发一次单项，重启保留单发，单项再拒不循环；欠费 400 不拆 |
| `backend/internal/app/curation_model_usage_integration_test.go` 和 `backend/internal/platform/postgres/migrations_rollback_integration_test.go` | 旧总额含 web、自定义旧子额度、null 总限及多个日期做 up/down/up；旧总输入 100/web 输入 30 仍总计 100。旧七天等待解除，其他延后项原样。模拟整理与独立艺人记同一本账、管家对话为零、在途实耗正常入账，触限保住原项和队列，调高或次日日界后原项继续。旧 API 样本的 web 字段同步更新 |
| `backend/internal/app` 的设置/进度 API 及回滚夹具 | 并发默认 4 与 1/16 边界、每日默认 10000000、单次默认 128000；PUT 字段缺省/null/类型错，未配置、配置错误原因、建议换模型、上游/本地额度、重试时刻和本机提示的返回形状；非管理员和跨源 POST 被拒。旧界面由主代理同版移除 web 字段 |

测试代理新增用例建议按 `TestCurationModelRules…`（单元）和 `TestPostgreSQLCurationModelRules…`（集成）命名。实际用例存在后，按场景选择运行，不用空匹配作为验证结果，例如：

```sh
(cd backend && mise exec -- go test ./internal/curation/model ./internal/steward/provider ./internal/steward/runtime -run '^TestCurationModelRules')
ROOMUSIC_TEST_RUN='^TestPostgreSQLCurationModelRules' mise exec -- ./scripts/test-integration.sh
```

主代理确认设计末节两处口径后，将相应边界补入测试预期。各步已验证且后续未改动的路径复用原结果。
