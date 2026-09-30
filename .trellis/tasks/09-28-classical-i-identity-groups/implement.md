# 阶段 I 执行计划

按 [design.md](design.md) 分六步实施，每步单独提交到 `main`（不推送），每步都可单独回退。
测试名以 `TestPostgreSQL` 开头的会被 `./scripts/test-integration.sh` 选中。

## 通用门禁（每步结束时）

```bash
cd backend
gofmt -l .                      # 输出为空
go vet ./...
go build ./...
go test ./... -count=1
go test -race ./... -count=1
cd .. && ./scripts/test-integration.sh
git diff --check
```

本任务改动扫描主流程、身份写入与读取投影，影响面无法用局部测试覆盖，因此每步都跑后端全量与 PostgreSQL 集成测试。
第 1、5 步可以先跑本步最小集合定位问题，最后仍跑全量。

## 第 1 步：解析器内容指纹

- [x] `backend/internal/library/parser/fingerprint.go`（新）：`ContentFingerprint(ctx, path)` 按扩展名分派；
  `m4a-mdat`、`mp3-frames`、`wav-data`、`flac-frames` 流式 SHA-256，按块检查 context。
- [x] `parser.go`：`AudioObservation` 增加 `ContentFingerprint`；`parseFLACStreamInfo` 读取 MD5（非零时生成 `flac-md5`）；
  `parseOggStream` 在已读入的数据上生成 `ogg-audio`。
- [x] `parsertest`：增加带指定 MD5 与音频帧字节的 FLAC 夹具。
- [x] 单元测试 `fingerprint_test.go`：`TestContentFingerprintIgnoresTags`（逐格式表驱动）、
  `TestContentFingerprintDetectsAudioChange`、`TestFLACFingerprintFallsBackWhenMD5IsZero`、
  `TestMP3FingerprintStripsLeadingAndTrailingTags`、`TestOggFingerprintIgnoresCommentPagination`、
  `TestM4AFingerprintHashesAllMediaData`、`TestContentFingerprintHonorsCancellation`。
- [x] `real_music_readonly_test.go`：增加 `TestRealMusicFingerprintCoverage`（未设置 `ROOMUSIC_REAL_MUSIC_ROOT` 时跳过）。
- 最小验证：`go test ./internal/library/parser -count=1`。
- 只读真实库核对（规划批准即授权本次运行；只读、只输出汇总计数）：
  `ROOMUSIC_REAL_MUSIC_ROOT=$(realpath ../music) go test ./internal/library/parser -run TestRealMusicFingerprintCoverage -count=1 -v`。
  结果写入任务 `research/`，不含路径与文件名。
- 提交：`feat(parser): 计算与标签无关的音频内容指纹`。

## 第 2 步：暂存表归入 catalog（行为不变的重构）

- [x] `backend/internal/catalog/staging.go`（新）：从 `library/scanner.go` 移入暂存、按 scope 分页读取、清理、CUE scope 对齐；
  library 改为调用它们，路径安全校验留在 library。
- [x] 测试名单与移动前逐项对账，没有丢失。
- 提交：`refactor(backend): 扫描暂存表归入 catalog`。

## 第 3 步：迁移 0013 与曲目重绑定

- [x] `backend/migrations/0013_track_content_fingerprints.sql`：design 第 2 节的曲目列、约束调整与暂存规划列；实施前在测试库
  用 `pg_constraint` 核对 `UNIQUE(source_root_id, relative_path)` 的实际约束名。
- [x] catalog：`SourceObservation` 增加大小、修改时间、指纹（不进 JSON）；暂存写入规划列；`Fingerprinter` 端口；
  `PlanStagedRoot`（design 第 4 节 a–e）；`PersistCandidate` 按计划写曲目（先腾出再占用），Release 暂时仍按锚点查找；
  删除 `legacySourceIdentity`、`legacyCueSourceIdentity` 与同路径兜底。
- [x] library：遍历记录大小与修改时间（CUE 虚拟轨取父文件）；调用规划并记录诊断与日志；指纹失败令 root 不完整；
  写入时跳过暂缓的 scope；实现 root 内的 Fingerprinter。
- [x] 测试调整：用“暂存 → 规划 → 写入”的测试辅助函数（`persistTestObservations`、`saveAudioObservation`、`planStagedObservations`）
  替换 `persistOrganizedCandidates`、`saveObservation`、`saveObservationWith`（它们只被测试使用，一并删除）；删除
  `TestPostgreSQLFirstRepairScanReusesLegacyCueTrackIdentity` 与只测旧身份函数的 `TestLegacyCueSourceIdentityCanBeReusedAfterUpgrade`。
- [x] 新增集成测试（真实临时目录中的合成 FLAC/CUE，走完整扫描流程）：
  - `TestPostgreSQLRescanKeepsTrackIdentity`（未变化重扫）
  - `TestPostgreSQLDirectoryRenameRebindsTracks`
  - `TestPostgreSQLMovedFileKeepsTrackIdentity`（同 root 内移到另一个专辑目录）
  - `TestPostgreSQLSwappedFilesFollowContent`
  - `TestPostgreSQLIdenticalAudioIsNotRebound`（歧义，记 `identity_ambiguous`）
  - `TestPostgreSQLIncompleteScanDefersMovesUntilCompleteScan`（不重绑定、不置缺失、不产生新身份；随后的完整扫描重绑定）
  - `TestPostgreSQLCanceledScanDoesNotRebind`
  - `TestPostgreSQLCrossRootMoveGetsNewIdentity`
  - `TestPostgreSQLReplacedContentGetsNewIdentity`（新 ID、旧曲目 missing、记 `content_replaced`）
  - `TestPostgreSQLCueDirectoryRenameRebindsVirtualTracks`
  - `TestPostgreSQLCompleteRootRebindsWhenAnotherRootIsUnavailable`（按 root 判断完整；整轮 incomplete，不置缺失）
  - `TestPostgreSQLUnchangedFilesReuseStoredFingerprints`（可计数的 Fingerprinter，第二次扫描不再计算）
  - `TestPostgreSQLFingerprintFailureDefersScope`
  - `TestPostgreSQLSingleTrackCueMergedTrackKeepsIdentityOnRename`（单轨 CUE 合并进物理轨的区间事实不影响配对）
  - `TestPostgreSQLScanAfterMigrationBackfillsFingerprintsWithoutChangingIdentity`（迁移后第一次扫描只补指纹，不换 ID）
  - `TestPostgreSQLIncompleteScanDefersReplacedContent`（不完整遍历中换内容的 scope 也暂缓）
- 提交：`feat(catalog): 按内容指纹在同一 root 内保守重绑定曲目`。

## 第 4 步：迁移 0014 与 Release 成员关系匹配

- [x] `backend/migrations/0014_release_anchor_location.sql`：删除 `releases_candidate_anchor_uq`，暂存增加 `planned_release_id`。
- [x] catalog：规划增加 Release 分配（design 第 6 节）；`PersistCandidate` 按 `planned_release_id` 复用或新建，复用时更新锚点。
- [x] 新增集成测试：
  - `TestPostgreSQLAddedEarlierFileKeepsReleaseIdentity`（散落专辑与同目录拆分，最小路径改变）
  - `TestPostgreSQLRemovedSmallestFileKeepsReleaseIdentity`
  - `TestPostgreSQLDirectoryRenameKeepsReleaseIdentity`
  - `TestPostgreSQLMovedFileJoinsTargetRelease`（两个 Release 的 ID 都不变）
  - `TestPostgreSQLSplitReleaseKeepsIdentityForLargerPart`（含平局：留在原位置的一方沿用）
  - `TestPostgreSQLMinorityOverlapCreatesNewRelease`（2 轨加 3 轨新建；新旧 Release 同锚点）
  - `TestPostgreSQLExactlyHalfOverlapIsNotAMajority`（1 轨加 1 轨：恰好一半不算过半）
  - `TestPostgreSQLTrackPathUniquenessOnlyConstrainsBoundRows`（0013 的约束：路径唯一性只约束仍绑定的行、指纹长度检查）
- [x] 第 3 步的矩阵测试补上 Release ID 断言（未变化重扫、改名；“移动”由 `TestPostgreSQLMovedFileJoinsTargetRelease` 覆盖）。
- 提交：`feat(catalog): Release 按曲目成员关系匹配`。

## 第 5 步：ReleaseGroup 只读接口

- [ ] `backend/internal/catalog/groups.go`（新）：列表与详情投影；`projection.go` 增加组摘要与详情类型。
- [ ] `backend/internal/catalog/httpapi/groups.go`（新）：两个 handler；`handlers.go` 的 `Register` 注册路由。
- [ ] `backend/internal/app/routes_test.go` 加入两个路由。
- [ ] `backend/internal/app/release_groups_api_integration_test.go`（新）：
  - `TestPostgreSQLReleaseGroupListMirrorsReleaseList`（总数、顺序、代表版本逐项对应；搜索、attention、分页、present-only）
  - `TestPostgreSQLReleaseGroupRepresentativeAndCounts`（直接插入多成员组：年份、创建时间、无年份排后；成员数与 attention 之和）
  - `TestPostgreSQLReleaseGroupDetailErrors`（401、400 `invalid_id`、404 不存在与无可见成员）
- [ ] `.trellis/spec/backend/catalog-rest-contracts.md` 增补按组接口合同与测试命令。
- 最小验证：`go test ./internal/catalog/... ./internal/app -run 'Release|Catalog|Artwork|Group|Routes' -count=1`，再跑全量门禁。
- 提交：`feat(catalog): ReleaseGroup 只读接口`。

## 第 6 步：文档与规范同步

- [ ] `.trellis/spec/backend/database-guidelines.md:90`–`91`：身份规则改为指纹重绑定与成员关系匹配。
- [ ] `.trellis/spec/backend/core0-runtime-contracts.md`：扫描流程（规划阶段、暂缓）、身份规则（`:110`–`112`）、四类新诊断、
  错误矩阵新增行。
- [ ] `.trellis/spec/backend/directory-structure.md`：暂存表归 catalog。
- [ ] `docs/architecture/adr/0002-metadata-overlays.md`：决策记录补充 2026-09-30“按 root 判断完整”的裁决；第四节相关行标为已由阶段 I 实施。
- [ ] `docs/architecture/core-1-roadmap.md`、`core-1-architecture.html`：阶段 I 状态与“当前基线”段落。
- [ ] 父任务 `prd.md`、`implement.md` 的阶段 I 状态；本任务 `prd.md` 增加“实施结果”。
- 验证：修改过的 Markdown 相对链接有效；`python3 ./.trellis/scripts/task.py validate .trellis/tasks/09-28-classical-i-identity-groups`；`git diff --check`。
- 提交：`docs: 同步阶段 I 的身份规则与按组接口`。

## 风险文件与回退点

| 风险 | 位置 | 应对 |
| --- | --- | --- |
| 扫描主流程改动大 | `library/scanner.go`、`catalog/persist.go` | 第 2 步先做纯搬迁；第 3、4 步分开提交，每步全量测试 |
| 身份腾出与交换的顺序 | `catalog/persist.go` | 同候选内与跨候选交换都有测试 |
| 迁移约束名 | `0013` | 实施前在测试库核对，`DROP CONSTRAINT IF EXISTS` 用核对后的名称 |
| 首次扫描读盘成本 | 迁移后的第一次扫描 | 真实库核对记录读取量与耗时；常规重扫复用已存指纹 |

每步提交都是回退点。迁移只能前向修复，不写 down migration。

## 启动前检查

- [ ] 用户明确批准最终规划摘要。
- [ ] `python3 ./.trellis/scripts/task.py validate .trellis/tasks/09-28-classical-i-identity-groups` 通过。
- [ ] `implement.jsonl`、`check.jsonl` 含真实规范条目。
- [ ] `python3 ./.trellis/scripts/task.py start .trellis/tasks/09-28-classical-i-identity-groups`。
