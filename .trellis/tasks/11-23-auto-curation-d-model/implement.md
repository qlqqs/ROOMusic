# 自动整理 D：实施计划

分支 `feat/auto-curation-d`。三次派发，各用 `gpt-6.2-astra`（high）实现、另一个代理写测试；前端解码器与 API 样例由主代理做。每次派发之后验收越界改动，再进入下一次。

## 派发 1：运行时与管家层

- 范围：迁移 0064 与 down；`steward/runtime`（`RunParams` 新字段、后台名额、`CancelBackground`、`Wait`、`escalated`、第 21 个调用、用量回调、`SkipMemory`、`SystemSuffix`、`kind` 的存储与守卫、`ResolveBackgroundPlans`）；`steward/tool`（`SourceCuration`）；`steward/httpapi`（`resolveModelConfig`、`StartBackground`、`BackgroundAvailability`、`GET /admin/curation/conversations`、`409 curation_conversation`/`curation_plan`、`CancelBackground` 两处调用）；审查第 2 版与 `app/steward.go` 的证据读取器（外部对应与候选，读 `curation` 现有函数）。
- 验证：`go build ./...`；`go test ./internal/steward/...`；集成 `TestPostgreSQLStewardHTTPC7|TestPostgreSQLStewardAppC7|TestPostgreSQLSteward.*Conversation`。
- 测试代理：`TestSupervisorBackgroundSlot`、`TestBackgroundTwentyFirstSharedCallRejected`、`BeforeStep`/用量回调/`Escalate` 的运行时单测；`TestPostgreSQLStewardCurationConversationHiddenAndLocked`、`TestPostgreSQLStewardReviewPolicyV2Evidence`、`TestPostgreSQLStewardReviewV1ApprovalRejected`。

## 派发 2：curation 写入层

- 范围：外部源与写入者拆分（`ExternalSource`、`linkState.provider`、`sourceSpec` 放行 `model`、`JoinGroup`）；`Request`/`LinkRequest`/`JoinRequest` 的 `ExpectedRevision`、`ConversationID`、`ConfirmedBy`；待定项（`Upsert` 重开搁置、领取、收尾）、例外、用量、模型设置的存储；`program.ModelMatcher`（`applyMatch` 拆分、派生键、写入后更新工作状态）；`curation/stewardtools`；`SuppressExternalLink`。
- 验证：`go test ./internal/curation/...`；集成 `TestPostgreSQLCuration|TestPostgreSQLB3`（程序层回归）。
- 测试代理：`ModelMatcher` 的计划与写入（外部源仍是 `musicbrainz`、来源 `model`、摘要更新、硬条件拒绝、重放）；`Upsert` 重开；`ConfirmedBy`；`SuppressExternalLink`。

## 派发 3：调度器、装配与接口

- 范围：`curation/model`（调度、收尾、重审退避、启动恢复、`ModelStatus`、`ExceptionResolver`）；`app` 装配（白名单、`correct_release` 改道、`Launcher` 适配器、调度器在 `registerSteward` 之后启动、测试夹具 `disableModel` 默认 true）；`curation/httpapi`（设置、进度、例外三个接口）；`dependency_rules_test.go`；API 样例。
- 验证：`go test ./internal/curation/... ./internal/app/...`；集成 `TestPostgreSQLCurationModel|TestPostgreSQLCurationException|TestPostgreSQLCurationProgram|TestPostgreSQLCurationSources`；`ROOMUSIC_UPDATE_API_SAMPLES=1` 生成新样例。
- 测试代理：design 第 10 节表里 R1–R8、R11、R12 的集成用例，以及四项变异检查。

## 主代理

- 前端：`features/steward/api/decoders.ts` 补 `curation_stopped`、`escalated`、可选 `kind`；`features/curation/api/decoders.ts` 补设置的两项、进度的 `model` 段、例外与会话列表的解码器与端点（界面留给 E2）；`test/api-contract.test.ts` 登记新样例；typecheck、eslint、相关 Vitest。
- 文档：`agent-and-operation-guidelines.md` 的“已决定、尚未实现：后台自动整理”改写为已实现；`steward-runtime-contracts.md`、`steward-review-contracts.md`（第 2 版全文同步）、`curation-contracts.md` 新增第 17 节；ADR 0006 加“D 说明”决定行（主代理按授权取的决定）；父任务 `implement.md` 交付记录；路线图。

## 回退点

- 每次派发验收后在分支上本地提交一次；任一派发失败可回到上一次提交。
- 迁移 0064 的 down 有损（删自动整理会话与例外），写进合同。
