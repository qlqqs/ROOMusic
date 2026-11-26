# 模型调用的通用规则

依据本任务 PRD、父任务 B4.1、B4.2 和 ADR 0009 §6.1。交付物是后台模型调用合同，设置界面由主代理使用本合同实现。

## 1. 改动位置与交接

下表路径相对仓库根目录。同一行的文件名相对其目录。

| 目录 | 实施涉及的文件 | 职责 |
| --- | --- | --- |
| `backend/internal/steward/provider/` | `errors.go`、`client.go`、`probe.go`、`stream.go`（仅在 eventStream.Next 重建错误时传递分类、稳定码和恢复提示） | 从 HTTP 或流错误取出分类、上游恢复时间和安全原因；复用已选协议的小工具探测 |
| `backend/internal/steward/runtime/` | `run.go`、`run_types.go` | 在后台请求边界组装实际提示、工具及输出上限，整步读取和短重试，传递结构化结束结果 |
| `backend/internal/steward/httpapi/`、`backend/internal/app/` | 前者的 `background.go`、`settings.go`、`handlers.go` 和后者的 `curation_model.go`、`steward.go` | 按开启者解析真实配置、探测、取得请求大小和逐项结果。应用装配层将管家类型映射到整理类型 |
| `backend/internal/curation/` | `settings.go`、`model_api.go`、`model_usage.go`、`model_queue.go`、`pending_items.go`、`work_states.go`、`model_exceptions.go`（首次结案计次）、`verification.go`（输入变化清空重试标记）；新增 `model_failures.go` | 设置、共享额度、模型暂停、项级事务、可领取查询与重开。目标互斥复用 `model_targets.go` |
| `backend/internal/curation/model/` | `status.go`、`queue.go`、`item.go`、`scheduler.go` | 领取前检查、分批、发送、错误处置、到时唤醒；`Launcher` 是跨包接口 |
| `backend/internal/curation/httpapi/` | `handlers.go` | 管理员的设置、进度和测试/重试 |
| `backend/migrations/` | 新增同号 `NNNN_curation_model_rules.sql` 和 `NNNN_curation_model_rules.down.sql` | §5 的一次事务迁移。基线最后一版为 0097，数字按父任务实际合入顺序取下一号 |
| `.trellis/spec/backend/` | `curation-contracts.md`、`steward-runtime-contracts.md`、`steward-config-contracts.md` 中相关段 | 规则、字段、错误与重试边界。整理合同重点是 §17、25.4、26、28.1、28.7；整篇重写属子任务 11 |

子任务 6 提供来源暂停状态，沿用 `backend/internal/curation/program/model_queue.go` 的 `LookupTierContext` 与 `sources.QueueRecoveryTime`。子任务 8、9 提供各类内容、所需来源和逐项答案，子任务 10 在独立艺人队列调用共享额度与模型放行接口。子任务 9 负责删除巡检、旧档位、联网项、未采用建议和审查不可用退避。本次只接通通用执行路径，沿用审查方案单独收尾的分支；不借五类错误迁移清队列。程序整理、管家对话与真实模型试用不在实施范围。旧审查重试与方案保留沿用现有分支（scheduler.go 第 642–706 行所涉旧流程），新的主调用按分类在该分支外结算。

## 2. 从一项待办到一次调用

1. 由整理开启者的管家设置提供端点、协议及凭据，`curation_settings.model` 有值时覆盖模型名，空串继承。可用性沿用 `BackgroundAvailability`，覆盖后的模型、协议用来运行和测试。缺必填配置时返回 `not_configured`，三类均不建会话，程序按其结论继续。密钥可选，解密失败与明确的工具能力拒绝有各自原因。
2. 每个空出的名额按**本地辅助 → 识别判断 → 名字与艺人核对**取当前可运行项。三类共用一个模型，沿用并发默认 4、可设 1–16，以及同发行/同艺人互斥。三类对应现有 `local_cleanup`、`release_match/recording_match`、`verify_*` 的入口，SQL 的分类与 Go 的 `taskKind` 同步。名字/艺人同级，不同回答合同分包。旧巡检、自查、联网领取暂保留独立的原入口，不能混入新三类的包；9 合入后去掉。这里定的是可运行项的先后，同一目录先辅助再交程序由 8 接入。
3. 将“先选出待办再建会话”的 `SelectionItems → prepareSelection → ClaimPreparedSelection` 用到三类。在预留目标、开会话前检查开关、开启者、扫描、全局模型状态、实际依赖和额度。依赖是该项准备或执行确需的来源：纯本地辅助及已有完整证据的纯本地判断可继续，需要读 MB 明细的项查共享的 `musicbrainz` 暂停时刻。不能只用 `Sources.Check` 的已启用状态，也不能把整个识别队列一律挂起。已暂停时把该项的 `next_attempt_at` 设到来源恢复时刻；准备中首次遇到暂停时沿用 `DeferSelection`，其余可运行项继续。准备完成、调用 `CreateConversation` 前再检查一次。验收统计建会话调用数，随后删掉空会话也算建过。
4. §4 编出可发的完整请求，校验身份摘要、目标和预留，按实际包用 `FOR UPDATE SKIP LOCKED` 领取。准备期间变动的项退出本包。每个拆出的包使用独立会话及自己的历史。发送前再检查模型状态和当日余量；额度刚耗尽则交还领取。完成后按 `item_id` 和 `ModelClaimDigest()` 逐项结算；已经成功的行不随批内其他行重来。进程恢复沿用 `RecoverInProgress`。
5. `runtime.RunEnd` 已有 `Reason/ErrorKind/StatusCode/Message`；当前 `backgroundRun.Wait` 丢掉后三项。扩展整理的 `Run.Wait` 结果，携带结束原因、五类之一、供应方稳定错误码、状态码、脱敏原因及可空的恢复时刻。需要在 `Start` 之前返回的配置错误也走这条映射。适配器负责解释协议，调度器负责写状态。每次实际请求的已知 usage（含失败和短重试）只入账一次。存储失败、主动取消、扫描开始、输入已变等按原业务原因收尾，不制造模型故障。出站明确被本机策略拒绝（egress_denied）作为需要改配置的原因显示。

## 3. 五类报错和计次

优先认出明确的认证、模型、工具及余额信号，再看普通 4xx、429 和 5xx；HTTP 200 的协议错误事件也检查其 `error.code/type`。错误正文只在适配器分类时读，保存与展示沿用 `failure` 的脱敏。显式的 401/403 总作配置错误，402 总作额度耗尽。

| 类别 | 判定与例外 | 处置与状态归属 |
| --- | --- | --- |
| 请求被拒 | 其余可归于该请求的 4xx，如 400、413、422；普通 404 不代表模型不存在。优先排除能力拒绝、余额不足及超时 | 单项记 `shelved/request_rejected`、安全原因“模型拒绝了请求”、当前模型及本地输入标识。到本地文件变或换模型才重开。多项普通 400 先按 §4 拆，只有逐项拒绝才累计。连续十个不同项相同拒绝升为全局配置问题 |
| 配置问题 | 401、403，或 `authentication_error/permission_error`、`model_not_found`、已核实的 Ollama 模型缺失文字、`tool_unsupported` / “does not support tools”；探测明确返回 `unsupported`。单次 `unconfirmed` 没有证明不支持工具 | 立即停止后续模型领取/发送，当项留队。全局存 `configuration` 和具体原因，无自动重试。有效模型配置改动或设置页测试成功解除 |
| 上游额度耗尽 | 402；400/429 或流事件明确返回 `insufficient_quota`、`insufficient_balance`、`billing_hard_limit_reached`，或文字明确“余额不足”“额度已用完”“insufficient balance”“exceeded your current quota”。普通 “quota” 或 “rate limit” 不足以判定欠费 | 全局存 `provider_quota`、原因、`retry_at=now+1h`，当前领取交还。到时只向有效模型发一次小请求，管理员也可手动触发；同一时刻最多一个探测。成功解除，仍欠费则再等一小时；明确新故障按其类别记录。与本地每日限额分别显示 |
| 临时故障 | 普通 429、500、502、503、504、529；`rate_limit_error/rate_limit_exceeded`、过载错误、`timeout`（含 408）、`unreachable`、`stream_interrupted`。主动取消优先于传输超时 | 一次逻辑请求含首次最多 5 发，自首次起 10 秒内发起重试。有效 Retry-After（秒或 HTTP 日期）或重置时刻优先，无提示按现有 300 ms 起翻倍至 3 s。用尽则交还项，全局存 `temporary`、`retry_at` 与退避级别；无提示时 5、10、20…最多 180 s。对方明确要求更晚时尊重其时刻。同一冷却期的并发失败只延后，成功一次清零下次的退避 |
| 回复格式错误或空返回 | `invalid_response`、协议正常结束但没有该项要求的结果、无效 JSON/参数、缺行或重复行、输出截断未成完整决定。B2 允许文本 JSON，不能“一律没调用工具就算空”。本地证据过期、服务端写库失败仍走相应业务出口 | 逐项 `empty_count` 加一，前两次保持待处理，`next_attempt_at` 按 5 s 起翻倍，封顶 180 s。第三次 `shelved/no_result`，原因“模型没给出结果”，本地文件变或换模型再试。连续十个不同项空返回，暂停全局并提示“这个模型可能不支持工具调用，建议换一个”，由改设置或测试成功恢复 |

**重试的执行边界。** 现有 `adapter.do` 只看到 HTTP 响应头，`runState.readModel` 才看得到流中断。对整理调用把重试预算放在读取**同一模型步骤的完整流**外，每次底层 HTTP 只发一次，先解析失败正文再决定重发。每次重建流解析器，后台事件暂存在本次尝试中，丢弃失败尝试的部分文本与工具片段，保留该次实际 usage；完整收到的一步工具操作最多执行一次。重置头仅解析有明确单位的表示，例如 OpenAI 的 `x-ratelimit-reset-requests/tokens` 时长或 Anthropic 对应的 `*-reset` 时间戳。多项有效限制取需等待的较晚时刻。交互对话仍走原有协议重试合同。新后台机制与旧 throttle observer 只能有一个发布最终失败。不得将 429 欠费先重发五次。

**“项的次数”与“连续项数”。** `attempts` 记已被回答的项。成功决定和明确“定不了”各计一次，后者在待办 evidence 的 decision_reason 保存理由，并以 resolution=undetermined 收尾，交回程序已有结论，不调用旧的升档方法。旧工具/新回答解析器都应在完成该项的事务中确认结果；不能仅从 `RunEnd=completed` 推断全批成功。五类失败均不增加 `attempts`。重试次数、`empty_count`、来源故障与 token 分别记录。有效答案清该项空返回计数；本地输入或模型换代也清零。网络错误没有给出回复，期间保留该项的空返回计数。

全局按**逐项结果提交顺序**计连续，不按会话数或 HTTP 次数。同一类连续段最多记十个不同的项 ID；同项的第二、三次空返回只改自己的计数，第一轮十个不同项皆空即能暂停。同样拒绝的键优先用状态码加具体的上游 code/type，仅有通用 invalid_request_error 或无稳定码用状态码及归一化的脱敏原因（折叠空白、略去请求标识）。不同拒绝原因开启新段，有效答案（含定不了）清空段；没有项结果的网络/来源等待不算一项。用数据库单行锁把本项更新及连续段变更放在一个事务中；已失去领取/摘要不符的重复收尾不能再累计。已在途的有效答复可按原绑定落地，不能顺带解除已经锁定的配置/余额暂停。

## 4. 单次最多发送与凑批

设置 `max_request_tokens` 表示这次模型输入与预留输出共同可用的上下文预算，默认 128000。采用可复现的近似估算，不要求安装各家分词器。对所选协议实际发送的紧凑 JSON 进行估算：连续 ASCII 字母数字每 4 个向上取整为 1，ASCII 标点、分隔符和空白每个计 1，非 ASCII 每个 Unicode 字符按其 UTF-8 字节数除以 2 向上取整。计入系统提示、任务说明、消息/历史、未信任资料包装、工具描述和 schema、各项完整证据与协议包装；沿用协议组包代码，暴露只编码不发送的大小估算，避免调度器另写一套提示。

实现时从运行时已有的构造步骤提取新会话的首个 provider.Request：用同一调用者可见工具表、实际任务后缀和同一首条用户消息构造，建会话前可在内存计算。适配器抽取 Stream 现用的协议编码，估算和发送共享编码结果或同一构造函数；创建/领取后若素材变动，丢弃旧预估重新组包。子任务 9 的识别判断须反映实际无工具的请求。

设预算为 L，预留输出 R = min(8192, max(1, floor(L/8)))，其中 8192 是运行时现有每次输出上限。实际传给模型的输出上限同为 R；输入估算加 R 小于或等于 L 方可发。按实际 usage 做日账，估算只用于凑批。对不同模型/语言，估算值会与真实分词数有差异；服务商明确拒绝仍走 §3。小上下文可配小值，防止固定预留 8192 挤光本机模型的输入空间。

- 取同一可运行类别与同一回答合同的项，按现有优先级、入队时间、ID 顺序选种子，再在同优先级的预选窗口（复用 `BatchSize` 默认 5）内逐项试装。每加一项测一次完整请求；放不下的留队，继续试窗口内更短的下一项。下一批仍由最老的合格项起头，长项也能单独发。共享说明和工具定义只计一份。识别判断与名字核对可合批，本地辅助按 `directory_key` 整目录一份，读取完整目录，窗口不得截断同目录内容。
- 单项先测完整输入。超限时仅在发给模型的副本上按 `program.Candidate.Score` 降序，同分保留程序原序，最多留前三个，再从末尾减至一个；无候选则直接检查原本地内容。保留完整本地标签、文件名、目录、注释、曲序、时长和冲突，允许提交的候选严格限定为实际发送的集合。仍超限就在建会话前记 `shelved/content_too_long`，显示“内容太长”，零调用。输入/模型或大小设置变动后重新估算。没有跨目录拆本地辅助。
- 整批收到**请求类 HTTP 400** 时给该批待办打 `single_request` 标记并交还，按原顺序每项各发一次，独立会话与额度/来源检查。一次单项 400 按请求被拒终止。标记随该输入代次，恢复进程或中途日限后仍单发，直至完成或输入更新。欠费/工具不支持的 400 走各自全局分支。批内漏答只重来漏答项，不触发整批单发。

## 5. 状态、升级与回滚

| 持久化位置 | 字段与写入时机 |
| --- | --- |
| `curation_settings` 唯一行 | 新增正整数 `max_request_tokens bigint NOT NULL DEFAULT 128000`。沿用 `model_daily_token_limit`，默认 10000000，保留自定义值及 NULL 不限。设置的 `paused` 仍由管理员控制，模型故障另记 |
| 新单行表 `curation_model_state` | 当前有效配置的版本标识、有效模型标识、`pause_kind`、`reason_code`、安全 `reason_detail`、`retry_at`、无提示退避秒数，以及最近连续段的类型/拒绝键/不同项 UUID 数组（最多十个）。无故障初始行。配置标识只在服务端生成，由开启者、端点、有效协议/模型、能力配置和凭据版本派生；可用保存的密文版本作摘要输入，绝不返回密钥或摘要。使用较窄的模型标识（端点、协议、有效模型）重开项。比较上一有效模型与本次值的状态更新、重新放出故障项、清除进行中空答累计/单发标记使用同一事务；只有令牌或工具能力改动时只清全局配置锁。新配置收到旧在途结果时按原代结算用量，忽略旧暂停 |
| `curation_pending_items` | 复用 status、attempts、shelved_reason、resolution、next_attempt_at 及领取摘要；新增 `empty_count`（0–3）、`model_stop_code`（可空）、`single_request`（默认 false）。安全理由按现有 shelved_reason 的 1000 字节约束截到完整字符。程序刷新仍用文件身份判定本地变化（与 5 的文件摘要对接）；同一输入单纯重新投影、候选顺序变化不能复活已拒绝或无结果项。换有效模型时定向复位这些标记。大小上限升高只主动重开因大小搁置的行。已决项及其审计结果保留 |
| `curation_model_usage` | 沿用按 day 唯一的 `input_tokens/output_tokens` 和事务 UPSERT。已有 `RecordItemUsage` 每次都累加总量，web 两列只是其中子集，例如总输入 100、联网输入 30 的旧行升级后共享总输入仍为 100。`TodayUsage/NextBudgetDay` 按现有调度时钟的本地日界工作 |

**迁移步骤。** 在同一事务增加上述新状态；删除 0091 的 `web_daily_token_limit`、`web_input_tokens`、`web_output_tokens`。数据库规范要求 down 恢复旧版本依赖的结构与数据合同，未要求保留已取消的联网子额度及历史分项。总用量原值直接沿用，不回填相加、不将旧子额度加进总额度。旧 attempts 含未处理次数，不能推导新 empty_count；新计数从零开始，尚在队列中且没有结论的行将旧 attempts 归零，已结束和旧搁置的历史记录保留。只对 0097 标记 `reset_item_usage_on_claim=true` 的旧触限等待解除七天时间闸门，保留领取时重置旧档计数的语义；重查实际来源恢复时刻再领取。其余来源等待时间、旧重审方案和待处理行的排队身份继续沿用。`item.go` 的推迟与重启恢复均改为按来源时间，不再写七天。旧单项档位上限随 9 的旧执行器移除；新三类执行器从上线起只用本设计的单次大小。

**共享额度。** 对子任务 10 暴露单独的读取当日用量/限额判定与 `AddModelUsage` 接口，抽出 `BeforeModelKind` 里的通用日额判断。独立艺人队列使用自己的启停和权限，不需伪造整理开启者或 pending item；使用同一有效模型时读取同一模型暂停状态，结算走同一日账。仅关闭整理开关并不等于关闭独立艺人功能。整理、艺人联网都在开新工作前判断 `input+output >= limit`，达到时保持队列和项次数。已发请求按实际 usage 正常记账，故并发在途可使实耗超过设置值，不预扣估算。刷新日界或调高总额立即重新检查，成功保存设置后唤醒相关队列。小探测的已知 usage 也记总账，本地额度已耗尽时自动探测等到有余额。管家普通聊天沿用自己的运行入口。

**版本和回滚。** 本子任务发布的是模型调度合同第 1 版，通过本迁移号区分数据库结构。没有音乐识别或解析算法升级，不递增当前 MatchRuleVersion 104、LocalRuleVersion 3、PatrolPromptVersion 1、资料版本 101，也不重扫、不批量重新询问历史模型结论。子任务 2、3、5、8、9 各自负责其版本。迁移只影响调度与设置，可先用合成库做 up → down → up。部署回退走仓库的迁移前备份和已登记 down SQL；按 0091 原有类型和约束重建三列：旧联网额度为 NULL（不限），旧联网输入及输出均为 0。移除本任务新列和状态行。降级后，共享总量仍完整，历史联网分项只能从升级前备份找回；新暂停/连续记录、已归零的旧未处理次数和解除的七天期限不能重建。需要准确恢复升级前的队列快照时用迁移前备份。应用和设置页面同版发布/回滚，老页面的解码器要求旧 web 字段。

## 6. 设置页接口

接口前缀为 `/api/v1/admin/curation`，沿用管理员及写请求同源校验。界面只组合下列合同。

| 接口 | 请求与返回 |
| --- | --- |
| `GET/PUT /settings` | 保留 `model`、`daily_token_limit`（1–10¹² 或 null）、`model_concurrency`（1–16，默认 4）。新增 `max_request_tokens` 正整数（同用 1–10¹²，缺省保留，null 无效）。移除 `web_daily_token_limit`；旧 PUT 的未知字段按现有严格解码报错。成功保存返回新设置并唤醒调度器 |
| `GET /progress` 的 `model` | 沿用 `model` 有效模型名、`state`、`unavailable_reason`、`running/concurrency`、`items`、`daily_token_limit`、`today.{date,input_tokens,output_tokens}`。删 `web_today/web_daily_token_limit/web_limit_reached/web_retry_at`。新增 `configured` 布尔、`pause_kind`（null、configuration、provider_quota、temporary、empty_replies、daily_limit）、`reason_detail`（脱敏文字或 null）、`retry_at`（RFC3339 或 null）、`can_retry` 布尔、`context_hint`（文字或 null）。`configured=false` 且原因 `not_configured` 显示“未配置模型”。开关状态与模型是否配置分别投影，未启用且无开启者时不借用当前访问者的配置。上游欠费与本地日限均显示具体来源，前者提示下次探测，后者显示下个日界；设置页可同时看到在途数和暂停原因 |
| `POST /model/retry` | 无请求体，用当前开启者保存的有效配置，以现有出站客户端和已选协议发一次不写库的 `roomusic_probe` 工具调用，复用 `probeTools`；不重新枚举模型/协议或探测联网，不建整理会话。仅检查凭据/端点有效性，不让历史的“不支持工具”结论在测试之前就短路。拿到预期工具调用及完整结束才清配置/空返回锁并唤醒。上游余额自动探测只需极短、完整的有效回复；探测失败保留或更新实际原因/下次时间。领取到期探测时锁住状态行，先把下次自动时刻移到一小时后再出站；手动与自动共用调度器的探测互斥，提交结果时核对配置代次。工具探测成功须使下次 BackgroundAvailability 与 StartBackground 所读的有效能力同步恢复：按端点、协议和有效模型更新后台结论；若整理指定另一模型，不覆盖管家对话模型的探测结论。用户明确覆盖为不支持时展示需要修改的配置。返回 200 和更新后的 `ModelProgress`，配置未齐或本地日限不允许请求时返回对应状态和 `can_retry=false`；HTTP 权限/存储错误按原约定 |

`context_hint` 给出“使用本机模型时，请按模型服务实际加载的上下文长度设置单次最多发送”，可始终随这里的设置提示展示。当前 `provider.Model` 列表只有模型标识、名称和部分搜索能力，预设名也不等于实际加载长度；不把 128000 显示成本机实测值。

## 7. PRD 范围对照

| PRD 行 | 设计 |
| --- | --- |
| 报错五类、出错不计次、空返回 | §3 的判定表、两种计数与重试边界 |
| 来源暂停不开会话 | §2 步骤 3、4 |
| 合并额度、去掉“用满后 7 天” | §5 的数据迁移、共享额度与到期恢复 |
| 单次最多发送、分批 | §4 的完整请求估算、目录单位、裁候选与 400 单发 |
| 任务先后、未配置模型 | §2 步骤 1、2 与 §6 |
| 设置页 | §6 的三个接口 |
| 规范 | §1 的规范清单，实施计划最后一步 |

## 已确认的实施口径

- 上游明确给出的恢复时刻优先，即使超过 180 秒。例如 Retry-After 为 600 秒时，到第 600 秒才可发送下一次请求。180 秒仅封顶本地指数退避。
- 设置页由 `context_hint` 提示按本机模型实际加载的上下文长度设置预算。本轮无需查询 Ollama、LM Studio 等服务的实际加载长度。
