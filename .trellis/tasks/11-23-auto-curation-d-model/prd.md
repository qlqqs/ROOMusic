# 自动整理 D：模型后台判断

## 目标

程序拿不准、留在待定项里的发行与单曲，由开启自动整理的管理员配置的模型在后台判断：查资料、选定匹配或修正字段，经独立审查后自动执行，写进自动层；审查没通过或转人工的，作为例外留给管理员采纳或不采纳。用量有每日上限，过程可在“自动整理”类型的会话里查看。

依据：ADR 0006 第 9、10 节（U5、U10、U11、U12、默认 4–6）；父任务 `design.md` 第七节、`implement.md` 的 D 一节；调研 `research/steward-current.md`（下文 R§n 指它的第 n 节）。

## 决定（主代理按 qlqq 的授权取定，规划子代理审阅；随交付列出）

下表是初定；规划子代理的修订（20 条，全部采纳）见 `design.md` 第 1 节，冲突处以那里为准。

| # | 决定 | 理由 |
| --- | --- | --- |
| D1 | D 只开放“匹配判定”与“发行级字段修正”两类写入。后台运行的工具集 = 只读工具（目录读取、音乐资料、新的待定项读取）+ 新工具 `resolve_release_match` + 现有 `correct_release`；归组、默认版本、问题核对、认领、艺人操作不提供给后台运行（I、G、H 再按各自子任务接入） | 待定项全是匹配问题；归组由匹配后的程序链按强证据做；艺人操作尚不可撤销，给了也只会转人工 |
| D2 | `resolve_release_match{item_id, external_id, basis}`：shared、可撤销、只在后台运行里列出。执行 = 程序按 ID 读回该 MusicBrainz 发行（单曲为录音），用程序现有的配对与写入链（外部对应、补空缺、强证据归组、封面）写入，来源 `{kind:model, provider:<规范化模型名>, basis}`，进 model 批次；工作状态记 `matched`、`method:"model"`，待定项结束为 `model_matched`。读回后曲目配对不上、或命中程序的相反证据中的硬条件（曲目数不同、介质数不同、已有指向别处的对应、标记过不再提出）时工具返回错误，不写 | 模型只做程序做不了的“选哪一个”，写入仍由程序保证一致；不替换已有对应与程序一致 |
| D3 | 模型认为都不对、无法判断时调用只读工具之外的非 shared 工具 `shelve_pending_item{item_id, reason}`，待定项转 `shelved`（不需审查，因为不改曲库）；证据摘要变化时程序把它重新放回 `waiting_model` | ADR U12“匹配不上的不反复重试”；不改数据的结论不需要审查 |
| D4 | 后台运行里 `correct_release` 写自动层（`Writer.Apply`，不带 `OnlyEmpty`）：`mode=clear` 拒绝；`expected_revision` 只核对不传入自动层；`basis.note` 截到 500 字节；`provider` 由模型名规范化（小写，非 `[a-z0-9._:-]` 换成 `-`，截 64）。对话与 MCP 照旧写人工修正 | ADR“工具名与参数不变，由运行的来源决定落层” |
| D5 | `steward_conversations.kind`：`chat`（默认）或 `curation`。会话列表、计数、未读都排除 `curation`；`curation` 会话模式固定 `steward`，改模式 `409`。管理员在自动整理会话里接着发消息时，那次运行是普通对话运行（来源 `chat`、写人工修正、不计每日上限、工具集照对话） | ADR“不混在对话列表里”“可以接着对话纠正”；人发起的即人的请求 |
| D6 | 后台运行由新调度器启动（单协程，形状照 `curation/program` 的循环）：总开关开、未暂停、无扫描、开启者仍是启用的管理员、开启者的管家配置可用（端点、协议、模型齐，密钥若有须能解开）、当日未达上限，才取一批。一批 = 至多 5 个 `waiting_model` 待定项（按创建时间），转 `in_progress` 并记会话 ID；第一条消息由程序生成（各项的 ID、证据、候选、冲突，外部文本标为不可信） | ADR 默认 4、6；5 项时一步的写调用正常不超过 20 |
| D7 | 停止：每步之前与每 60 秒检查上述条件（暂停、关开关、开启者失效、端点或密钥变化、达到上限）；不满足时跑完当前一步后结束（新结束原因 `curation_stopped`）。开启者登出、撤销会话不取消后台运行；开启者改端点或密钥、关开关、暂停会取消 | ADR 第 10 节停止条件；“不依赖登录会话” |
| D8 | 并发：后台运行不计入每用户 3 个；全实例至多 1 个后台运行（调度器单协程，`Supervisor` 另设一个后台名额） | ADR 默认 6 |
| D9 | 后台运行不读开启者的个人记忆，系统提示附加自动整理说明 | 提示词自足，不把个人偏好带进曲库整理 |
| D10 | 审查策略第 2 版（`ReviewPolicyVersion=2`，对话与后台都用）：证据的发行目标加 `external`——生效的外部对应（`curation.LinksOf`）与该发行未结束待定项的候选摘要，带来源标记 `program_untrusted_external`；后台运行里一步超过 20 个 shared 调用时，第 21 个起直接作为工具错误返回给模型（不整份转人工）；强证据类工具在 D 不提供给后台（D1），规则随 G、H 接入 | ADR 第 9 节第 2 版 |
| D11 | 审查不可用：调度器按 5 分钟、30 分钟、2 小时用 `ReviewPlanID` 重审至多 3 次；仍不可用则方案作废、各项回 `waiting_model` 并设 `next_attempt_at` = 24 小时后 | ADR“有限重试，仍不可用则停在等模型” |
| D12 | 例外：审查 `rejected` 或 `needs_human_confirmation` 的项，把提出的工具与输入、预览的前后值、审查结论与理由、审查 ID、会话 ID 记到待定项上，转 `waiting_human`；不随方案过期。一次运行结束时仍 `in_progress` 的项（模型没处理）回 `waiting_model` 并 `attempts+1`，第 3 次转 `shelved` | ADR 默认 5；防止同一项无限重跑 |
| D13 | 采纳例外：按当时的状态重算预览，前值与记下的不同则 `409 exception_stale`；相同则执行并记为管理员的人工确认操作——字段修正走人工修正（`governance.CorrectionExecutor`）；匹配走 D2 的写入链，来源 `kind:model` 不变、操作 origin 标 `review_decision:"human"`、不进 model 批次。不采纳：字段写字段值的不再提出标记、匹配写 `external_link` 标记，待定项结束为 `human_declined` | ADR 默认 5 |
| D14 | 用量：新表按日累计后台整理的输入与输出 token（主模型每步、审查、摘要都计）；“日”按服务器时区。上限存在 `curation_settings.model_daily_token_limit`（默认 10 000 000，NULL 为不限）；“整理用的模型”存在 `curation_settings.model`（空串 = 对话模型），审查模型用开启者自己的审查模型设置 | ADR U10、第 9 节身份 |
| D15 | 接口（E2 只做界面）：`PUT /admin/curation/settings` 增加 `model`、`daily_token_limit`；`GET /admin/curation/progress` 增加 `model` 段（状态、不可用原因、今日用量、上限、等模型/处理中/等人/搁置的项数）；`GET /admin/curation/exceptions`（分页）、`POST …/exceptions/{id}/accept`、`POST …/exceptions/{id}/decline`；`GET /admin/curation/conversations`（当前管理员名下的自动整理会话，分页）。单个会话的读取、事件、接着对话沿用管家现有接口 | 父任务把界面放在 E2 |

## 需求与验收

| # | 需求 | 验收（受控模型服务，现有管家测试的做法） |
| --- | --- | --- |
| R1 | 调度与会话 | 有待定项且条件齐备时启动一次后台运行，会话 `kind=curation`、`mode=steward`、归开启者；不出现在会话列表、计数、未读；改模式 409；第一条消息含各项 ID |
| R2 | 匹配判定 | 模型选定候选 → 审查通过 → 外部对应、补空缺、归组按程序链写入，来源 `model`、在 model 批次；待定项 `resolved/model_matched`；可按批与按条撤销 |
| R3 | 字段修正落自动层 | 后台的 `correct_release` 写自动层；同一工具在对话里仍写人工修正；`clear` 被拒 |
| R4 | 审查四条路径 | 通过 → 执行；没通过与转人工 → 例外（`waiting_human`，带理由）；不可用 → 按退避重审，三次后回 `waiting_model` 且 24 小时内不再取 |
| R5 | 搁置与未处理 | `shelve_pending_item` → `shelved`；证据变化后回到 `waiting_model`；未处理三次 → `shelved` |
| R6 | 停止条件 | 关开关、暂停、开启者停用或降级、清除或更换端点与密钥、达到上限：不再启动，正在跑的在当前一步后以 `curation_stopped` 结束；开启者登出不影响 |
| R7 | 用量与上限 | 主模型、审查、摘要的 token 都计入当日；达到上限后当日不再启动，次日（注入时钟）恢复；不限时不检查；对话里的用量不计入 |
| R8 | 例外处理 | 采纳：前值未变时执行并记为人工确认（字段进人工修正）；前值变了 409；不采纳：写不再提出标记，之后程序与模型不再提出同一值 |
| R9 | 审查第 2 版 | 证据带外部对应与候选摘要（来源标记）；后台一步第 21 个 shared 调用被拒为工具错误；对话 `steward` 模式除证据外行为不变；`ReviewPolicyVersion=2` 时旧版批准在执行前被拒 |
| R10 | 并发 | 后台运行不占开启者的 3 个名额；同时至多 1 个后台运行 |
| R11 | 接口 | D15 各接口的权限（非管理员 403）、校验与错误码；API 样例与前端契约同步（解码器随样例更新，界面留给 E2） |
| R12 | 不配置模型的实例 | 开启者没配管家时只做程序层；进度里 `model.state` 说明原因；现有程序层与对话的测试照过 |

权限与审批做变异检查：去掉“后台运行不计入名额”“写自动层的来源判断”“开启者核对”“上限检查”任一项，都有用例失败。

## 不做

- 前端界面（E2）。本任务只同步解码器与 API 样例。
- 归组、默认版本、问题核对、认领、艺人与发行结构操作的后台使用（I、G、H）。
- 站外通知；运行表与重启恢复（重启后在途的项按 D12 回到 `waiting_model`）。

## 规划决定方式

子代理审阅路线（qlqq 已授权子任务规划完直接实现）。规划子代理（Plan）对照代码审阅 D1–D15 并写出技术设计，主代理全部采纳（`design.md` 第 1 节）。

## 验证记录（2026-11-23）

- 派发 1 测试：`go test ./internal/steward/...`；集成 `TestPostgreSQLMigration|TestPostgreSQLStewardCuration|TestPostgreSQLStewardReview|TestPostgreSQLStewardBackground|TestPostgreSQLStewardHTTPC7` 通过（含 0064 往返）。
- 派发 2 测试：13 个集成用例（匹配器、写入、待定项、例外、用量、设置、后台工具）与单元测试通过；程序层回归 `TestPostgreSQLCuration|TestPostgreSQLB3` 通过。
- 派发 3 测试：15 个集成用例（R1–R8、R10–R12、重启恢复、例外续做）通过；四项变异各被检出（名额 → `BackgroundSlot`；落层改道 → `CorrectReleaseAutoLayer`；开启者核对 → `StopConditions/enabler_changed`；上限 → `DailyLimit`）。
- 测试发现并修复的实现缺陷：`curation.loadSettings` 外连接在行锁重查后读到空用户名导致 panic（改为分两次查）；最后一步进行中暂停时按完成计次（收尾时复查停止条件）。
- 全量：`go vet ./...`、`go test ./...` 通过；整套集成串行一次，2306 项通过，唯一失败 `TestPostgreSQLStewardC6Migration54RoundTrip`（旧迁移往返用例没回退 0064），修正后与 C7、平台迁移往返用例一起重跑通过。
- 前端：typecheck、`eslint src e2e` 通过；Vitest 全量 4575/4577 左右（与集成同时跑，`steward.test.tsx`、`agent-settings.test.tsx` 已知的负载敏感用例 1–3 个失败，单独运行 110 个全过）；契约测试登记 10 个新样例。
- 未做：真实模型服务与真实 MusicBrainz 下的后台运行（留给 V12）；界面（E2）。
