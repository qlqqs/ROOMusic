# 自动整理 D：技术设计

基线 `main` `3334e3a1`。路径省略 `backend/internal/`。初稿由规划子代理（Plan）对照代码写出并审阅 `prd.md` 的 D1–D15；主代理全部采纳，`prd.md` 的决定以本文第 1 节的修订为准。

## 1. 对决定的修订（审阅意见，全部采纳）

| # | 级别 | 问题（代码依据） | 修订 |
| --- | --- | --- | --- |
| D2-a | 必改 | `ApplyLinks` 把 `Source.Provider` 直接写进 `external_links.source` 与 `linkState.Source`（`curation/external_link.go:152,166`），撤销按它定位（`external_link_revert.go:43,60`）；`JoinGroup` 要求 `Provider=="musicbrainz"`（`join_group.go:22`）；`sourceSpec` 要求 `Kind=="source"`（`external_artwork.go:49`）。照原样用模型名会让对应落成 `source='claude-…'`，强证据（`join_group.go:73-77`）与 `planGroup` 失效 | 拆开“外部源”与“写入者”：`LinkRequest`、`JoinRequest` 加 `ExternalSource`（只允许 `musicbrainz`），`external_links.source` 与 `linkState.Source` 写它，`linkState` 另加 `provider`；`sourceSpec` 放行 `Kind=="model"`；`JoinGroup` 核对 `ExternalSource`。程序层传 `ExternalSource:"musicbrainz"`，行为不变 |
| D2-b | 必改 | `applyMatch` 是 `Worker` 未导出方法，绑定程序批次与程序的键（`program/enrich.go:182`、`unit.go:25,63`），产生至多 3 条操作；一次工具调用只有一个 `Caller.IdempotencyKey`，对账按它查（`run_tools.go:229`） | `curation/program` 导出 `ModelMatcher{Plan(ctx,item,externalID) (MatchPlan,error); Apply(ctx,plan,ApplyOptions)}`，`applyMatch` 拆成“计划”与“写入”共用。对应用调用键，补空缺与归组用派生键（UUIDv5，以调用键为命名空间、步骤名为名）。不需要建立对应时返回错误、不写 |
| D2-c | 必改 | 工作状态只改 `status` 不改 `input_digest`，写入后摘要变了，`needsWork` 会让程序重跑（`pass.go:132,239-255`） | `Apply` 写入后在事务里重读证据，用 `ReleaseDigest(fresh)` 写 `matched`、`method:"model"`，同 `saveConclusion`（`pass.go:307-358`） |
| D4-a | 必改 | 外部写入的 provider 正则 `^[a-z0-9._-]` 不含 `:`（`external_artwork.go:30`），`Writer.Apply` 的含（`writer.go:40`） | 规范化：非 `[a-z0-9._-]` 换成 `-`，截 64，为空用 `model` |
| D7-a | 必改 | 复查遇 `ErrRunInvalidated` 调 `CancelUser(run.userID)`（`supervisor.go:137-139`），会连带取消开启者自己的对话；`CancelUser` 跳过后台后，改端点或密钥（`steward/httpapi/settings.go:158`、`probe.go:122`）就取消不到它 | 后台运行复查失效只取消自己；新增 `CancelBackground(userID)`，在上面两处与 `CancelUser` 一起调；`CancelUser` 跳过后台运行（登出、撤销会话不影响） |
| D7-b | 必改 | “跑完当前一步”与“改端点密钥、关开关、暂停会取消”矛盾；另一位管理员重新开启时 `enabled_by` 换人，`Writer` 每次写入重读 `enabled_by`（`writer.go:58`），运行的 `Caller.User` 仍是原来的人，（操作者, 键）对账失效 | 立即取消（`cancelled`）只用于端点或密钥变化；关开关、暂停、开启者停用、降级或换人、达到上限、扫描中、MusicBrainz 源关闭都在下一步之前以 `curation_stopped` 结束。三个后台工具处理器核对 `caller.User.ID == enabled_by`。`Writer` 不检查暂停，暂停前已批准、正在执行的这一步仍会写入，写进合同 |
| D12-a | 必改 | steward 模式转人工的项让方案保持 pending、运行以 `awaiting_confirmation` 结束（`run_review.go:401-403,453-457`）：一项转人工停掉整批，还留下一份能经对话 `decision`、`review` 接口确认的方案，绕过 D13 的重校验 | 后台运行在运行中直接转例外：审查后由 `Escalate` 钩子写例外，转人工的方案项取舍记新值 `escalated`，工具结果 `{code:"escalated_to_human", exception_id, reasons}` 交给模型，运行继续；没通过的同样写例外，取舍仍是 `rejected_by_review`。`steward_plans` 加 `background`；后台方案调 `decision`、`review` 一律 `409 curation_plan` |
| D12-b | 建议 | 同一项可能有匹配与字段两份提案；接口的 `{id}` 宜指向例外本身 | 例外单独建表 `curation_model_exceptions`，指向待定项；待定项只留调度用的列 |
| D12-c | 建议 | 不是每种结束原因都该计“未处理次数” | 只有 `completed`、`step_limit`、`token_limit` 计 `attempts+1`；`cancelled`、`curation_stopped`、审查重试用尽不计；`provider_error` 不计，调度器自身退避（10 分钟起倍增，至多 6 小时） |
| D1/D4 | 必改 | 后台 `correct_release` 能改任意发行，外部文本的提示注入可借它扩大范围 | 后台只允许改本次运行处理中的待定项所属发行，否则工具错误 `out_of_batch` |
| D4-b | 建议 | 事务外核对 `expected_revision` 有竞态；人工修正优先，自动值写了也不生效（`curation-contracts.md` §4 末条），审查看到的前后值误导 | `curation.Request` 加 `ExpectedRevision`（`LockAutoRelease` 之后核对）与 `ConversationID`（进 origin）；有人工修正的字段返回工具错误 `manual_override` |
| D3 | 必改 | `UpsertPendingItem` 只更新 `waiting_model`（`work_states.go:95`），搁置项回不来 | 条件改为 `status='waiting_model' OR (status='shelved' AND input_digest<>excluded.input_digest)`，后者置回 `waiting_model`、`attempts=0` |
| D5 | 建议 | 自动整理会话里发新消息会作废 pending 方案（`messages.go:117-119`）；删会话后处理中的项悬空 | 调度器重审时方案已不在 → 项回 `waiting_model`，不计次数；会话下还有处理中的项时删除返回 `409 conversation_busy` |
| D6 | 建议 | 漏了 ADR §10：MusicBrainz 源关闭只停这个源、暂停与扫描在出站前生效；待定项可能属于已隐藏的发行 | 启动条件与每步检查加“MusicBrainz 源开、无扫描”；取项 join `VisibleReleaseIDsSQL`（同 `progress.go:44-48`）；第一条消息只放 ID、种类与冲突代码（程序生成，可信），证据与候选经只读工具取回，由 `Untrusted` 走现有不可信包装（`run_content.go:185`） |
| D1 | 建议 | 后台工具集不应含 `web_search`、`fetch_page`、自带搜索与记忆工具；后台的音乐资料工具属于后台请求（ADR §10） | 白名单（第 5 节），`NativeSearch=false`；后台调用音乐资料工具先过 `Sources.Check("musicbrainz")` |
| D13-a | 必改 | `Writer` 的操作者固定是开启者（`writer.go:57-63`、`external_artwork.go:52-59`），做不到“记为采纳的管理员”；`insertSuppression` 的 `$6` 没有 `NULLIF`（`suppressions.go:151`），空撤销 ID 转换失败 | `Request`、`LinkRequest`、`JoinRequest` 加 `ConfirmedBy`：非空时操作者是它（须是启用的管理员），不核对开启者、不允许带批次。新增导出 `SuppressExternalLink`（带 `NULLIF`）；单曲不采纳时标记打在曲目对象上 |
| D13-b | 建议 | 按来源整体撤销会扫到带 provider 的操作（`operations/curation_candidates.go:20`） | 采纳的字段修正 origin 写 `{source:"curation", review_id, review_decision:"human", conversation_id}`，不带 provider，不被按来源整体撤销卷入；采纳的匹配保留 `kind:model` |
| D14 | 建议 | `curation_settings.updated_at` 是程序层的变化标记（`work_states.go:30`、`worker.go:257`），改模型设置会触发全量程序扫描 | 改模型字段不动 `updated_at`，另记 `model_updated_at` |
| D15 | 必改 | `curation/httpapi` 不能导入管家包（`app/dependency_rules_test.go:63`） | `GET /admin/curation/conversations` 在 `steward/httpapi` 注册；采纳匹配要用 `program`，经 `curation.ExceptionResolver` 接口注入 |
| R11 | 必改 | 前端 `endReasons`（`frontend/src/features/steward/api/decoders.ts:342`）与方案项 `decision`（`:289`）是固定枚举 | 补 `curation_stopped`、`escalated`，会话加可选 `kind`。origin 解码器不动：`source` 取 `curation`，`review_decision` 本来接受任意字符串（`governance/api/decoders.ts:372-379`） |

D8、D9、D10、D11 可行，细节见下文。

## 2. 架构与包边界

| 包 | 职责 | 依赖 |
| --- | --- | --- |
| `curation` | 待定项状态、例外、用量、模型设置的存储；外部源与写入者拆分；`Request`/`LinkRequest`/`JoinRequest` 新字段；`ModelStatus`、`ExceptionResolver` 接口；`ModelRun` 上下文值 | 不变 |
| `curation/program` | 导出 `ModelMatcher`，`applyMatch` 重构为程序与模型共用 | 不变 |
| `curation/model`（新） | 调度器 `Scheduler`、`Launcher` 端口、提示词、收尾、例外的采纳与不采纳（实现 `ExceptionResolver`） | `{curation, curation/program, catalog, catalog/governance, operations, platform/uuid}`，不导入管家包 |
| `curation/stewardtools`（新） | `read_pending_items`、`resolve_release_match`、`shelve_pending_item`；后台用的 `AutoCorrectRelease` 处理器 | `{curation, curation/program, catalog, catalog/governance, operations, platform/uuid, steward/tool}` |
| `steward/runtime` | `RunParams` 新字段、后台名额、`escalated`、第 21 个调用规则、用量回调、`kind` | 不变 |
| `steward/tool` | `SourceCuration = "curation"` | 不变 |
| `steward/httpapi` | `configured` 拆出 `resolveModelConfig(ctx,userID) (UserSettings,string,error)`（返回 `*ModelUnavailable{Reason}`，`configured` 只把错误写成现有 HTTP 响应）；导出 `StartBackground(ctx, BackgroundParams) (*runtime.Run, error)` 与 `BackgroundAvailability(ctx,userID) (model, reason string, err error)`；会话列表路由 | 不变 |
| `app` | 装配：工具白名单、`correct_release` 改道、证据第 2 版、`Launcher` 适配器、启动调度器 | 加两个新包 |

- **不经 ResponseWriter 构造 Provider**：`StartBackground` 依次 `resolveModelConfig(enabler)` → `OutboundClient`（开启者是管理员，策略不受限）→ `preflight` → `modelProvider` → `supervisor.Start(lifecycleCtx, RunParams{Background:true, …})`。`Check` 只比较端点与密钥密文快照（复用 `sameModelKey`，`events.go:209`）。模型名取 `curation_settings.model`，空则开启者设置的 `Model`；审查模型取开启者的 `ReviewModel`。
- **运行上下文**：`Launcher` 适配器把 `curation.WithModelRun(ctx, *ModelRun{ConversationID, Batch func(ctx)(string,error)})` 放进传给 `Start` 的 ctx（`WithoutCancel` 保留值，`supervisor.go:101`），工具处理器能取到；批次在第一次写入时才开。对话续聊没有这个值，后台工具一律 `permission_denied`。

## 3. 迁移 0064

```sql
-- 会话
ALTER TABLE steward_conversations ADD COLUMN kind text NOT NULL DEFAULT 'chat'
  CHECK (kind IN ('chat','curation')),
  ADD CONSTRAINT steward_conversations_curation_mode_ck CHECK (kind <> 'curation' OR mode = 'steward');
CREATE INDEX steward_conversations_curation_idx ON steward_conversations (user_id, updated_at DESC, id) WHERE kind = 'curation';
ALTER TABLE steward_plans ADD COLUMN background boolean NOT NULL DEFAULT false;
ALTER TABLE steward_plan_items DROP CONSTRAINT steward_plan_items_decision_check,
  ADD CONSTRAINT steward_plan_items_decision_check CHECK (decision IN ('pending','accepted','declined','rejected_by_review','escalated'));
-- 待定项
ALTER TABLE curation_pending_items
  ADD COLUMN conversation_id uuid REFERENCES steward_conversations(id) ON DELETE SET NULL,
  ADD COLUMN claimed_at timestamptz,
  ADD COLUMN attempts smallint NOT NULL DEFAULT 0 CHECK (attempts BETWEEN 0 AND 3),
  ADD COLUMN review_retries smallint NOT NULL DEFAULT 0 CHECK (review_retries BETWEEN 0 AND 3),
  ADD COLUMN next_attempt_at timestamptz,
  ADD COLUMN shelved_reason text CHECK (octet_length(shelved_reason) <= 1000),
  ADD CONSTRAINT curation_pending_items_claim_ck CHECK (status <> 'in_progress' OR claimed_at IS NOT NULL);
CREATE INDEX curation_pending_items_due_idx ON curation_pending_items (status, next_attempt_at, created_at);
-- 例外
CREATE TABLE curation_model_exceptions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  pending_item_id uuid NOT NULL REFERENCES curation_pending_items(id) ON DELETE CASCADE,
  plan_item_id uuid NOT NULL UNIQUE,            -- 不设外键：删会话会级联删方案
  tool text NOT NULL CHECK (tool IN ('resolve_release_match','correct_release')),
  input jsonb NOT NULL, preview jsonb NOT NULL,
  provider text NOT NULL CHECK (provider ~ '^[a-z0-9._-]{1,64}$'),
  review_id uuid NOT NULL, review_model text NOT NULL,
  review_decision text NOT NULL CHECK (review_decision IN ('rejected','needs_human_confirmation')),
  review_source text NOT NULL CHECK (review_source IN ('model','policy')),
  risk text CHECK (risk IN ('low','medium','high')), reasons jsonb NOT NULL DEFAULT '[]',
  conversation_id uuid REFERENCES steward_conversations(id) ON DELETE SET NULL,
  status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','accepting','accepted','declined')),
  decided_by uuid REFERENCES users(id), decided_at timestamptz, operation_ids uuid[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((status = 'open') = (decided_by IS NULL)),
  CHECK ((status IN ('accepted','declined')) = (decided_at IS NOT NULL)));
CREATE INDEX curation_model_exceptions_open_idx ON curation_model_exceptions (status, created_at DESC, id);
-- 用量与设置
CREATE TABLE curation_model_usage (day date PRIMARY KEY,
  input_tokens bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
  output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
  updated_at timestamptz NOT NULL DEFAULT now());
ALTER TABLE curation_settings
  ADD COLUMN model text NOT NULL DEFAULT '' CHECK (octet_length(model) <= 200),
  ADD COLUMN model_daily_token_limit bigint DEFAULT 10000000 CHECK (model_daily_token_limit IS NULL OR model_daily_token_limit > 0),
  ADD COLUMN model_updated_at timestamptz;
```

`resolution` 取值：`program_matched | model_matched | human_accepted | human_declined`（都符合已有正则）。

down：删两张新表；待定项 `in_progress`、`waiting_human` 改回 `waiting_model` 后删新列与约束；方案项 `escalated` 改 `declined`、恢复 0055 的约束、删 `background`；`DELETE FROM steward_conversations WHERE kind='curation'`（级联消息与方案；操作记录里的会话 ID 只是 JSON 值）后删 `kind` 列、约束、索引；删 `curation_settings` 三列。

## 4. 运行时改动

```go
// RunParams 新增
Background   bool                                   // 后台运行，占全实例唯一的后台名额
BeforeStep   func(context.Context) error            // 返回 ErrRunStopped → EndCurationStopped
Usage        func(context.Context, UsageKind, provider.Usage) // main | review | summary
SkipMemory   bool
SystemSuffix string                                 // 自动整理说明，接在模式提示之后
Escalate     func(context.Context, PlanItem, Review, ReviewItem) (exceptionID string, err error)
```

- 结束原因新增 `EndCurationStopped = "curation_stopped"`。`BeforeStep` 在 `run.go:283` 循环顶部调用，`ReviewPlanID` 分支 `reviewPlan` 之前（`run.go:158`）也调一次。
- 用量：`recordUsage`（`run.go:447`）调 `Usage(main)`；摘要也经 `recordUsage`（`summary.go:104`），另传 `summary`；`callReview` 返回后（`run_review.go:342`）调 `Usage(review)`。只知一项时缺的按 0。
- `SkipMemory` 时跳过 `run.go:120-127`。
- 转人工：`Background` 时 `run_review.go:401` 的循环不再 `continue`；转人工与没通过的项先调 `Escalate` 取例外 ID，转人工的记 `escalated`，再交 `executePlanItems`；`run_tools.go:236` 旁加 `escalated` 分支输出例外 ID。`CreatePlan` 把 `Background` 写进 `steward_plans.background`。
- 第 21 个调用：`run_tools.go:33` 加判断 `Background && len(pending) >= 20` → 该调用直接失败 `code="too_many_proposals"`，不预览、不进方案；`reviewPolicy` 的“超过 20 项”保留给对话。
- 后台名额：`Supervisor.Start` 遇 `Background` 不计入每用户 3 个，已有未结束的后台运行则 `ErrBackgroundBusy`；`Run` 加 `background`；复查失效对后台只 `run.cancel()`；`CancelUser`、`CancelMembers` 跳过后台；新增 `CancelBackground(userID)`；`Shutdown` 照旧全取消；导出 `(*Run).Wait(ctx) (RunEnd, error)`。
- 存储：新增 `CreateCurationConversation(ctx,userID,title,model)`；`List`、`Count`、`CountUnread` 加 `AND kind='chat'`（`conversations.go:75-112,322-338`）；`UpdateConversation` 改 `kind=curation` 的模式 → `ErrCurationConversation`（`409 curation_conversation`）；`CheckPlanDecision` 遇后台方案 → `ErrCurationPlan`（`409 curation_plan`）；新增 `ResolveBackgroundPlans(ctx, conversationID|"", "superseded")`，复用 `resolvePendingPlans` 的对账（`plan_store.go:305`）。

## 5. 工具

后台白名单在 `app`：`Caller.Source == "curation"` 时名单外工具 `Available=false`，`Invoke` 再查一次（`registry.go:93`）。名单：目录只读工具（`get_release`、`get_release_evidence`、`get_album`、`get_track`、`search_library`、`list_*`）、`search_music_metadata`、`get_music_metadata`、三个新工具、`correct_release`。三个新工具只在 `Source=="curation"` 时列出，定义在 `curation/stewardtools`，在 `app/steward.go:98` 附近注册，同样经 `withCurrentStewardUser` 包装。

| 工具 | 影响 | 输入 |
| --- | --- | --- |
| `read_pending_items` | read，`Untrusted` | `{"item_ids":[id36]}`（可省，省略返回本次运行全部处理中的项）→ `[{item_id, kind, release_id, conflicts, evidence, candidates}]` |
| `resolve_release_match` | shared，可撤销，MinRole admin | `{"item_id":id36,"external_id":id36,"basis":{"note":1–500}}`，三项必填 |
| `shelve_pending_item` | personal（非 shared，直接执行） | `{"item_id":id36,"reason":1–500}` |

- 共同前置：有 `ModelRun`；`caller.User.ID == enabled_by`；该项 `status='in_progress'` 且 `conversation_id` 是本会话。
- `resolve_release_match`：预览调 `ModelMatcher.Plan`（按 ID 读回发行或录音，走 30 天缓存 `sources/cache.go:101`，多次预览摘要相同），核对 D2 的硬条件与“该项 `input_digest` 与当前证据一致”（否则 `item_stale`）；`changes` 依次是外部对应、曲目对应数、各补空缺字段的前后值、归组目标。执行调 `Apply`（`Source{model, provider, basis 截 500 字节}`、`ExternalSource:"musicbrainz"`、`ModelRun.Batch`），成功后同一事务把待定项结束为 `model_matched`；对应已指向同一 ID（崩溃重放）按成功处理。
- `correct_release` 改道：`app/steward.go:126` 的 shared 包装层判断 `Source=="curation"` → `curationtools.AutoCorrectRelease(writer)`：`ParseCorrectionChanges` 解析、拒绝 `clear`、检查本批与人工修正，再 `Writer.Apply{ExpectedRevision, ConversationID, Key: caller.IdempotencyKey, BatchID}`。对话与 MCP 仍走 `CorrectionExecutor`（`stewardtools/commands.go:28-34`）。
- 幂等：所有写入的操作者是 `enabled_by`，后台 `Caller.User` 也是开启者，`LookupOperationResult(userID,key)` 对得上；调度器启动时与每次工具调用都核对。

## 6. 调度器（`curation/model.Scheduler.Run`）

1. 启动恢复：`CloseOpenBatches("model")`；`ResolveBackgroundPlans(all)`；所有 `in_progress` 改回 `waiting_model`，清 `conversation_id`、`review_retries`、`next_attempt_at`，`attempts` 不变。
2. 循环（`Options{Now, PollInterval=1m, ReviewBackoff=[5m,30m,2h], …}` 可注入）：
   - 启动条件：`Settings.Active`、未暂停、无扫描、MusicBrainz 源开、`Launcher.Availability(enabler)` 可用、当日用量低于上限。
   - 先处理重审：有 `in_progress` 且 `next_attempt_at <= now` 的会话，读其 pending 后台方案，带 `ReviewPlanID` 启动；方案已不在则这批项回 `waiting_model`，不计次数。
   - 否则取新批：有 `waiting_model`、到期、发行可见的项才建会话（标题如“自动整理 11-23 14:05”），`UPDATE … SET status='in_progress', conversation_id, claimed_at WHERE id IN (SELECT … ORDER BY created_at,id LIMIT 5 FOR UPDATE SKIP LOCKED)`；一项没取到就删会话。
   - 第一条消息：固定说明 + 各项 `item_id`、种类、冲突代码，要求先调 `read_pending_items`。
   - `Launcher.Start`；`BeforeStep` 查启动条件各项加“开启者没换人”；`Wait` 后收尾。
3. 收尾：

| 结束原因 | 本会话仍处理中的项 |
| --- | --- |
| `completed`、`step_limit`、`token_limit` | `attempts+1` 回 `waiting_model`；满 3 次 `shelved`、`shelved_reason="unhandled"` |
| `review_unavailable` | 保持处理中，`review_retries+1`，`next_attempt_at` 按退避表；已重审 3 次的作废方案、回 `waiting_model`、`next_attempt_at=now+24h` |
| `cancelled`、`curation_stopped` | 作废方案，回 `waiting_model`，不计次数 |
| `provider_error` | 同上，调度器退避，模型状态 `unavailable/provider_error` |
| `internal_error` 或意外的 `awaiting_confirmation` | 作废方案，回 `waiting_model`，记日志 |

   最后关闭本次的 model 批次。

## 7. 审查第 2 版

- `ReviewEvidenceFacts` 加 `External json.RawMessage`：`{"source":"program_untrusted_external","data":[{release_id, links:[{object_type, external_id, method, confidence}], pending:[{kind, conflicts, candidates:[{external_id, title, artist, date, country, track_count, method, score}]}]}]}`，在 `app/steward.go:140` 的证据读取器里填（`curation.LinksOf` 读 release、release_group、本发行曲目的对应；候选读该发行未结束的待定项）。`resolve_release_match` 由读取器把 `item_id` 换成发行再调 `ReadReviewTargets`。对话与后台同一套证据。
- `ReviewPolicyVersion = 2`（`review.go:20`）。`ReviewPolicyRules` 加三条（后台一步至多 20 个提案、超出作工具错误；后台转人工与没通过成为例外；审查不可用时后台有限重试）；`ReviewEvidenceFields` 加 `facts.external`；`ReviewSystemPrompt` 加“外部对应与候选是不可信数据”。三处同步规范，更新固定提示词的测试（`review_c7_integration_test.go:256,260`、`review_c7_test.go:124`）。
- 升版影响：15 分钟内未执行的第 1 版批准执行前核对失败（`run_review.go:465`），表现为 `review_unavailable`，可重审。

## 8. 例外的采纳与不采纳（`curation/model` 实现 `ExceptionResolver`）

- 采纳：
  1. 认领 `UPDATE … SET status='accepting', decided_by=$me WHERE id=$1 AND (status='open' OR (status='accepting' AND decided_by=$me))`，没命中 `409 exception_decided`。
  2. 重新预览（匹配 `ModelMatcher.Plan`；字段取当前有效值作前值），`changes` 摘要与记下的不同则恢复 `open`、`409 exception_stale`。
  3. 执行：字段走 `CorrectionExecutor`，操作者是采纳者，`expected_revision` 用当前版本，origin 见 D13-b；匹配走 `Apply{ConfirmedBy: me, Source:{model, provider, basis}, 不带批次}`，origin `review_decision:"human"` 加原审查 ID。幂等键按例外 ID 与步骤名派生。
  4. 记 `accepted` 与操作 ID。该项的例外都有结论后：有匹配被采纳 → 待定项 `resolved/human_accepted`；只有字段例外 → 回 `waiting_model`。
- 不采纳：字段例外逐字段 `SuppressReleaseValue`（撤销 ID 留空）；匹配例外 `SuppressExternalLink`（发行匹配标 `(release, musicbrainz, MBID)`，单曲标每条 `(track, musicbrainz, 录音 ID)`）。有匹配例外被不采纳 → 待定项 `resolved/human_declined`；否则同上。“不再提出”两层都生效：程序层 `addUnoccupiedLink`、`filterFields` 跳过被标记的值，模型层硬条件与 `nothing_to_apply` 拒绝。
- 程序先匹配上：`ResolvePendingItems` 也结束 `waiting_human` 的项（`work_states.go:277`），未决例外在采纳时因前值变化 409。

## 9. 接口（D15）

全部要求管理员（否则 `403 forbidden`），写要同源，数据库故障 `503 database_unavailable`。

- `PUT /admin/curation/settings` 加 `model`（≤200 字节，空串 = 对话模型）与 `daily_token_limit`（1–10¹²，`null` 不限，省略不改）；响应 `Settings` 加两项；`400 invalid_input`。
- `GET /admin/curation/progress` 加：
  ```json
  "model":{"state":"idle|running|waiting_review|paused|disabled|waiting_for_scan|limit_reached|unavailable",
   "unavailable_reason":"not_configured|key_needs_reentry|tool_unsupported|enabler_unavailable|source_disabled|provider_error|null",
   "model":"…","today":{"date":"2026-11-23","input_tokens":0,"output_tokens":0},"daily_token_limit":10000000,
   "items":{"waiting_model":0,"in_progress":0,"waiting_human":0,"shelved":0}}
  ```
  状态来自 `curation.ModelStatus`（调度器实现），计数从库里读、只算可见发行。
- `GET /admin/curation/exceptions?status=open|all&page&page_size≤100` → `{items:[{id, pending_item_id, kind, release:{id,title,artist}, tool, input, preview:{changes}, review:{id, model, decision, source, risk, reasons}, conversation_id, status, decided_by, decided_at, operation_ids, created_at}], total, page, page_size}`；`400 invalid_input`。
- `POST …/exceptions/{id}/accept` → `200 {exception, operation_ids}`；`400 invalid_id`、`404 not_found`、`409 exception_decided`、`409 exception_stale`、`409 curation_disabled`（仅匹配）、`409 source_disabled`、`409 nothing_to_apply`、`503 source_unavailable`。
- `POST …/exceptions/{id}/decline` → `200 {exception}`；`400 invalid_id`、`404 not_found`、`409 exception_decided`。
- `GET /admin/curation/conversations?page&page_size≤100`（`steward/httpapi`）：当前管理员名下 `kind=curation` 的会话，`conversationResponse` 加 `kind` 与 `item_counts`。会话读取、事件、续聊、取消沿用 `/me/steward/...`；改模式 `409 curation_conversation`；后台方案 `decision`、`review` → `409 curation_plan`。

## 10. 风险与回退

- 提示注入：外部文本经不可信包装；写入限定本批发行；第 21 个提案被拒；全部提案经审查。同模型审查独立性有限，设置里已有提示（U11）。
- 限速：后台音乐资料工具与程序层共用 MusicBrainz 每秒 1 次，也与开启者本人对话共用每分钟 20 次的个人额度（`steward/web/tools.go:92-108`），对话可能变慢。
- 撤销：只撤对应这一条会让归组失去强证据，与程序层现有语义一致，写进规范。
- 时区：“日”跟随服务器时区（容器通常 UTC），界面注明（E2）。
- 回退：运营上关总开关、暂停或把上限调小即停模型层，程序层不受影响；代码上 `assembly.disableModel` 不启动调度器；数据上 down 迁移删自动整理会话与例外，已写的自动层可按来源撤销。
