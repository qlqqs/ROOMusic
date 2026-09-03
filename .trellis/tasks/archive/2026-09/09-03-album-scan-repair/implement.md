# 实施计划：专辑扫描解析与候选闭环

## 启动前门槛

- [x] 用户已明确批准最新 `prd.md`、`design.md` 和本文件；任务已运行 `task.py start`。
- [x] 记录当前 migration 版本、git 工作树和 PostgreSQL 18 集成入口；不读取或扫描真实
      `music/`，测试只使用合成 fixture。
- [x] 实施/检查 manifest 已包含 backend、frontend、数据库、真实资产安全和交叉层规范。
- [x] 先对 V0 参考行为建立测试输入/输出，不复制 V0 runtime、依赖或旧 SQL。

## 阶段 1：锁定纯合同与回归基线

- [x] 从 `audioObservation`/`sourceObservation` 提炼统一 observation、CUE relation、
      candidate anchor、field decision、audio facts 和 artwork observation 类型。
- [x] 为现有 FLAC/MP3/OGG/Opus/WAV 建立行为基线，补 track source identity 和
      AlbumArtist 传递测试。
- [x] 将 AC1--AC4、AC7 的 organizer case 写成 table-driven 测试：遍历顺序置换、
      majority/tie、same-dir split、unknown single、strict multidisc、Box leaf、
      filename fallback。
- [x] 阶段门槛：纯 organizer 输出的 candidate/decision/track identity 在输入排序变化
      下完全一致；未通过不得改 persistence。

## 阶段 2：修复 M4A 与 CUE parser

- [x] 实现有界 ISO-BMFF atom reader，替换 `os.ReadFile` 和总文件 16 MiB 拒绝；处理
      64-bit largesize、size=0、嵌套边界、raw iTunes atom key、AAC/ALAC facts。
- [x] 实现 CUE 文本编码解码与多 `FILE` 结构 parser；解析 sheet/track metadata、
      CATALOG、ISRC、INDEX offsets/end/duration，并对每个引用做 containment。
- [x] 扩展 observation 保存 CUE 父来源、引用文件、index/start/end、duration、codec 等，
      使 scanner 不再丢弃事实。
- [x] 增加小型合成 fixture：大于 16 MiB M4A、moov 前/后、AAC/ALAC、raw atom 标签、
      UTF-8/UTF-16/GBK/Shift-JIS/Big5 CUE、越界/缺失/截断输入；禁止引用真实 `music/`。
- [x] 阶段门槛：`go test ./cmd/roomusic -run 'Test(Parse|Cue|Audio)' -count=1` 通过，
      malformed case 不 panic，`go vet` 无新增问题。

## 阶段 3：落地 organizer 规则

- [x] 修正 tag > folder > filename 的字段来源优先级和 AlbumArtist 传递，删除目录名
      无条件成为 album 的行为。
- [x] 实现同目录按 album/album artist 的稳定 partition；少数冲突多数决，平票/无
      明显多数拆 candidate；严格多碟和 Box leaf 规则；anchor 不含可变 metadata。
- [x] 实现根目录 loose album/unknown 和显式 CUE 父子去重，真实分轨优先且多 FILE
      不全局过滤；补 credits/source semantics 的最小 decision。
- [x] 记录 grouping inconsistency、source refs 和 bounded candidates；为所有决定写
      `high|medium|low` 与 `auto_apply|uncertain_apply`。
- [x] 阶段门槛：AC1--AC7 纯规则测试通过，任意 observation 顺序输出相同；失败 case
      有明确 attention/reason code。

## 阶段 4：迁移、staging 与候选持久化

- [x] 新增 forward migration（编号按当前 schema 顺序），完成 legacy anchor/source
      identity backfill、candidate/medium/track/decision/evidence 唯一约束和 CUE/audio
      columns；migration 不编辑既有文件。
- [x] 增加 bounded `scan_observations` staging 批写、按 anchor 有序读取、取消/失败清理。
- [x] 将 `scanRoot` 改为“遍历/解析 -> staging -> organize -> candidate store”，解析
      失败不阻止其它有效 candidate，删除 `complete && ...` 的全-or-nothing gate。
- [x] 实现 candidate 短事务 upsert：Release/Group/Medium、物理/CUE source identity、
      Track 重挂、current decisions/credits/grouping evidence replace；旧空壳普通查询
      隐藏。
- [x] 将 artwork 绑定移动到 candidate ID 确定之后，验证 hash/MIME/storage key 和失败
      清理；不修改源文件。
- [x] 增加 PostgreSQL 18 集成测试：fresh/rerun 时间戳与 ID 稳定、事务回滚、candidate
      split/shape change、CUE identity/dedupe、evidence replacement、artwork binding、
      staging cleanup、complete-only missing。
- [x] 阶段门槛：`./scripts/test-integration.sh` 和
      `cd backend && go test -race ./cmd/roomusic -count=1` 通过；不通过不得接 REST。

## 阶段 5：REST 查询、权限与扫描诊断

- [x] 扩展 Release list/detail SQL 和 DTO：present-only、stable pagination/search、
      year/source/media/counts、attention 聚合与 `attention=required` allowlist。
- [x] 增加管理员 evidence endpoint；普通用户 403；候选/reason/source refs 有界且不含
      绝对路径；补 malformed ID、数据库错误、legacy evidence 缺失测试。
- [x] 扩展 scan status/diagnostics 聚合计数，保留旧 wire enum、取消和错误 envelope。
- [x] 阶段门槛：handler/数据库测试逐字段对齐；AC8--AC10 后端证据通过。

## 阶段 6：前端只读问题视图

- [x] 在 `frontend/src/api.ts` 为新增 DTO 实现严格 decoder：枚举、nullable、bounded
      arrays、403/empty/diagnostic 状态，不使用 raw cast。
- [x] 在 `main.tsx` 把 `q`/`attention` 放入 URL；列表显示 badge/filter，详情显示核心
      字段、Medium/Track facts、credits/evidence summary；管理员按需读取 evidence。
- [x] 管理区显示 scan diagnostics 聚合和 succeeded/incomplete/failed/canceled；所有
      问题入口提示修改外部源后重扫，不增加写入/confirm/merge/split 控件。
- [x] 增加 Vitest decoder/interaction 测试及窄屏/键盘可用性检查；构建时刷新嵌入资产。
- [x] 阶段门槛：`npm run lint && npm run typecheck && npm run test && npm run build`，
      AC10--AC12 前端证据通过。

## 阶段 7：跨层收口与审查

- [x] 对 AC1--AC12 建立测试映射，复核 parser -> staging -> organizer -> SQL -> REST ->
      decoder -> UI 的字段、权限、状态和 source identity 数据流。
- [x] 运行受影响模块完整门禁：
      `cd backend && gofmt -l . && go test ./... -count=1 && go vet ./... && go build ./...`；
      `cd frontend && npm run lint && npm run typecheck && npm run test && npm run build`；
      `./scripts/test-integration.sh`；`git diff --check`。
- [x] 使用 `trellis-check` 检查规范符合性、迁移约束、幂等/回滚、权限/路径脱敏、取消/
      incomplete/missing、只读源安全和未来 overlay seam；发现问题修复后重跑全范围检查。
- [x] 按项目文档语言约束更新相关 `.trellis/spec/`（若新增稳定合同/坑点），不把临时
      调试笔记留作英文或未归档文档。

## 风险与回滚点

- M4A atom 变体或第三方依赖未满足内存/许可证要求：停在阶段 2，保留旧 parser，补
  研究和 fixture 后再选择实现；不得降低 AC6。
- migration/backfill 失败：停止 scanner 写入，恢复数据库备份或 forward-fix；不运行
  破坏性 down migration，不让旧直属目录 writer 与新唯一约束并存。
- candidate persistence 失败：当前短事务回滚并记录诊断，已提交 candidate 保持可见；
  不把部分结果宣布为 succeeded，不执行 missing。
- REST/UI 字段或权限不一致：回退到兼容的可选字段/旧页面，修复 decoder 和 handler
  后再刷新嵌入资产；不在前端绕过后端权限。

## 完成前检查

- [x] `prd.md` 无阻塞问题且完成 PRD convergence pass。
- [x] `design.md` 覆盖模块责任、数据流、identity、事务、迁移、权限、回退和未来 seam。
- [x] `implement.jsonl` 与 `check.jsonl` 各有真实规范/研究上下文，`task.py validate`
      通过。
- [x] 用户在本最终规划摘要之后明确批准，随后才运行 `task.py start`。

## 验收证据映射

| 验收项 | 主要自动化证据 |
| --- | --- |
| AC1 | `TestOrganizeMajorityDeterministic`、`TestPostgreSQLCandidatePersistenceIsIdempotentAndReplacesCurrentEvidence` |
| AC2 | `TestOrganizeStrictMultidisc`、`TestOrganizeStrictMultidiscWithoutAlbumTagUsesParentFolder`、`TestOrganizeNonContiguousDiscDirectoriesStayLeafCandidates` |
| AC3 | `TestOrganizeSameDirectoryConflictUsesMajorityOrStableSplit`、`TestOrganizeMissingAlbumArtistRemainsCompatibleWithKnownRelease`、`TestOrganizerEvidenceListsAreBounded` |
| AC4 | `TestOrganizeLooseAndUnknown`、`TestOrganizeRootUnknownAndCueIdentityAreExplicit` |
| AC5 | `TestParseCueDocumentSupportsMultiFileMetadataAndIndexRanges`、`TestParseCueDecoderSupportsGBKShiftJISAndBig5`、`TestPostgreSQLScanRootAlignsCrossDirectoryCueParent`、`TestPostgreSQLCueStagingAlignsParentAndPersistsVirtualIdentity` |
| AC6 | `TestParseM4AReaderParsesLateMoovAndRawITunesAtoms`、`TestParseM4AReaderParsesALACConfigAndLargesize`、`TestParseExistingTagParsersRetainAlbumArtist`、`TestParseWAVReaderSkipsLargePayloadAndParsesFacts`、`TestParseOggAndOpusIdentificationFacts` |
| AC7 | `TestOrganizeTagAlbumOutranksFolderFallback`、`TestOrganizerFallbackPositionsPreserveExplicitTags`、`TestRipLogEvidenceFillsOnlyMissingReleaseSemantics`、`TestDiscoverCandidateRipLogEvidenceRequiresExplicitSignatureAndSkipsSymlink` |
| AC8 | `TestPostgreSQLCandidatePersistenceIsIdempotentAndReplacesCurrentEvidence`、`TestPostgreSQLScanRootPersistsCueSheetMetadata`、`TestPostgreSQLScanPersistsExplicitRipLogCDSemantics` |
| AC9 | `TestPostgreSQLCandidatePersistenceIsIdempotentAndReplacesCurrentEvidence`、`TestPostgreSQLCandidateTransactionRollsBackOnEvidenceFailure`、`TestPostgreSQLIncompleteRootPersistsValidCandidatesAndCleansStaging`、`TestMissingReconciliationRequiresSuccessfulTerminalState` |
| AC10 | `TestPostgreSQLCatalogReadProjectionAndEvidencePermissions`、`TestReleaseAttentionFilterAllowlist`、`frontend/src/api.test.ts`、`frontend/src/release_filters.test.ts` |
| AC11 | `TestFolderArtworkDoesNotFollowSymlink`、只读 REST/UI 代码审查；自动测试仅使用 `t.TempDir()` 合成来源，不扫描真实 `music/` |
| AC12 | `TestValidateLibraryPathEnforcesRealContainment`、`TestFileSymlinkTargetsMustRemainWithinRoot`、`TestScanRootReturnsDiagnosticPersistenceFailure`、`TestPostgreSQLScanRootRejectsNonRegularAudioSource`、`TestPostgreSQLArtworkLinkFailureRemovesNewManagedFile`、`TestPostgreSQLManagedArtworkIsRevalidatedAndCleanupErrorsPropagate`、`TestProductionFrontendServesRootFallbackAndAsset404` |

## 最终验证记录

2026-09-03 在最终工作树快照执行：

- `cd backend && test -z "$(gofmt -l .)"`
- `cd backend && go test ./... -count=1`
- `cd backend && go test -race ./... -count=1`
- `cd backend && go vet ./...`
- `cd backend && go build ./...`
- `cd frontend && npm run lint`
- `cd frontend && npm run typecheck`
- `cd frontend && npm run test`（2 个测试文件、10 个测试）
- `cd frontend && npm run build`
- `bash -n scripts/*.sh`
- `PG_PASSWORD=ci-placeholder MEILI_MASTER_KEY=ci-placeholder docker compose config --quiet`
- `PG_PASSWORD=ci-placeholder MEILI_MASTER_KEY=ci-placeholder docker compose -f compose.test.yaml config --quiet`
- `./scripts/test-integration.sh`（PostgreSQL 18，测试通过并清理专用容器与数据卷）
- `python3 ./.trellis/scripts/task.py validate .trellis/tasks/09-03-album-scan-repair`
- `git diff --check`

没有运行真实音乐 smoke：它是显式 opt-in 的只读辅助验证，不属于 CI 或本任务完成
门槛；本次所有音频/CUE fixture 均由测试在临时目录合成。

当前前端测试环境没有 jsdom/component runner，因此 Release A→B→A 的 generation
竞态由 effect 代码审查与纯 URL/payload 测试覆盖，未伪造组件级测试结论；如后续引入
组件测试基础设施，应把该竞态加入首批回归用例。
