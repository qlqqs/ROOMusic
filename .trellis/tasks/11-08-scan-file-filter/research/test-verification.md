# ① 文件过滤与失败处理：测试验证记录

日期：2026-11-08。仅补测试、修旧断言/夹具、重生 API 样本；未修改实现、未提交。

## 新增与修改的测试

| 文件 | 本轮覆盖 |
| --- | --- |
| `backend/internal/library/scan_filter_test.go`（新增） | 隐藏项、完整内置名单及大小写、整段匹配不误伤、用户排除子树、登记根自身不被过滤、预计数过滤 |
| `backend/internal/library/parser/cue_path_test.go`（新增） | NFC/NFD、大小写、唯一匹配、歧义停止、stem 回退的相同规则及保留 containment 校验 |
| `backend/internal/library/scan_file_filter_integration_test.go`（新增） | 真实 worker 的过滤/跳过/删除对账、非 root 的 chmod 000 子目录及其物理/CUE 轨保护、越界 CUE、保住曲目的 UUID 不被同指纹新来源抢走、三种空根情况、坏 cover.jpg 保留关系与受管文件 |
| `backend/internal/app/exclusions_integration_test.go`（新增） | 排除项 HTTP 添加/删除、原收据重放、同键异路径/异根 409、等于根/越界 400、100 条上限 409、目录列表 excluded_paths、目录内浏览 parent/selectable；沿用真实鉴权与请求边界 |
| `backend/internal/library/scanner_observation_test.go` | 保住语义及清理 SQL 替身；覆盖用户指定的三个失败传播回归用例 |
| `backend/internal/library/evidence_test.go` | 四个命名/优先级封面测试改用真正编码的 1×1 JPEG |
| `backend/internal/library/scan_rules_test.go` | 明确 M4A 已支持，不再列入不支持格式断言 |
| `backend/internal/library/identity_rebinding_integration_test.go` | 原先用损坏 MP3 制造不完整的两个测试改为：解析失败仍成功，正常移动配对和内容替换继续执行 |
| `backend/internal/library/scanner_persistence_integration_test.go` | 非普通文件只诊断、不入库，但不再令根不完整；有效 FLAC 与坏 MP3 混合时成功保存并清理暂存；保留原有未提交改动 |
| `backend/internal/platform/postgres/migrations_rollback_integration_test.go` | 0034 的非空 exclusions/held/skipped/exclude/unexclude 夹具；回滚不误删旧 create 收据及父记录 |
| `backend/internal/app/api_samples_integration_test.go` | incomplete 样本改由真实不可用根产生，不再错误依赖坏 FLAC；原根在后续扫描前恢复 |
| `frontend/src/features/library/api/scan-filter-decoders.test.ts`（新增） | excluded_paths、skipped_files、exclude/unexclude 操作类型及缺字段/非法字段拒绝 |
| `frontend/src/features/library/hooks/use-root-exclusion-commands.test.tsx`（新增） | 添加/删除请求、URL 编码、幂等键、网络失败手动重试复用原键、冲突、缓存失效 |
| `frontend/src/features/library/scan-status.test.ts` | 新 incomplete 文案；仅 succeeded 且 skipped_files>0 显示跳过提示 |
| `frontend/src/app/library-scans.test.tsx` | 页面级 incomplete 文案断言同步 |

`scan_lifecycle_integration_test.go` 现有不完整断言来自进程恢复，非 unsupported/parse failure，不应放宽，故未修改。其恢复/取消测试已真实执行。

API 主样本与 scan-feedback 样本分别重生 210/59 份；实际变化为 41 个 JSON，新增字段及 incomplete 夹具结果均来自真实 handler，未手工编辑 JSON。

## 隔离数据库与工具

- Compose 项目仅为 `roomusic-test-scan-s`，使用 `compose.test.yaml` 的默认 bridge，不新建网络。
- 随机库 `integration_12340`，本机随机端口 47083；无生产连接。
- 测试进程 UID 1000，chmod 000 用例在非 root 身份下运行，并先断言 `os.ReadDir` 真实返回权限拒绝，结束恢复权限。
- `ROOMUSIC_TEST_FFMPEG` 和 `ROOMUSIC_TEST_PG_BIN_DIR` 分别由本 worktree 的 `scripts/ffmpeg-pinned.sh`、`scripts/pg-client-pinned.sh` 获取。
- 通过指定 Compose 命令启动，未使用会碰共享项目的 `scripts/test-integration.sh`。

## 实际验证命令与结果

以下 Go 命令在 `backend/` 执行，数据库命令均加载本轮隔离库环境。**所有 PostgreSQL 用例均为 PASS，0 SKIP**。

```bash
# 单测：PASS。
go test ./internal/library/ -run 'TestScanFilter|TestScannerOnlyDiagnoses|TestFileSymlinkTargets|TestCueReferenceCompletion|TestScanRoot' -count=1
go test ./internal/library/parser/ -run 'Cue' -count=1
go test ./internal/library/ -run '^(TestArtistEvidenceScanRejectsOverflowWithDiagnostic|TestFolderArtworkMatchesNamesCaseInsensitively|TestFolderArtworkCaseOnlyVariantsPreferLowercaseThenByteOrder|TestFolderArtworkNamePriorityIgnoresCase|TestFolderArtworkSearchesDirectoriesInOrder)$' -count=1

# 扫描/保住/空根/身份/封面：PASS。
go test ./internal/library/ ./internal/catalog/ingest/ -run 'Held|EmptyRoot|Exclusion|ScanLifecycle|IdentityRebinding|ArtworkIntegration' -count=1 -v
# ingest 包本身该正则没有匹配用例，非数据库 SKIP；其新行为由上述真实扫描集成路径覆盖。

# HTTP 排除项五个用例全部 PASS。
go test ./internal/app/ -run '^TestPostgreSQLExclusion' -count=1 -v

# 0031–0034 回滚往返全部 PASS，含 0034 非空夹具。
go test ./internal/platform/postgres/ -run 'TestPostgreSQLMigrationRollbackRoundTrip' -count=1 -v

# 样本重生：两个测试全部 PASS。
ROOMUSIC_UPDATE_API_SAMPLES=1 go test ./internal/app/ -run '^TestPostgreSQL(APIResponseSamples|ScanFeedbackAPIResponseSamples)$' -count=1 -v

# 恢复、取消、暂存清理保护四例 PASS；非普通来源旧断言初次失败，修复后仅重跑该失败例。
go test ./internal/library/ -run '^TestPostgreSQL(ScanRootRejectsNonRegularAudioSource|ScanRootReportsTerminalStagingCleanupFailure|CanceledScanDoesNotReconcileMissingSources|CancelDuringScanSQLFinalizesAndReleasesHolder|ScanRecoverySkipsLiveHolder)$' -count=1 -v
go test ./internal/library/ -run '^TestPostgreSQLScanRootRejectsNonRegularAudioSource$' -count=1 -v
# 审查发现并同步的旧 parse failure 断言：精确运行 PASS。
go test ./internal/library/ -run '^TestPostgreSQLScanLifecycleParseFailurePersistsValidCandidatesAndCleansStaging$' -count=1 -v

# 静态检查：PASS。
go vet ./internal/library/... ./internal/catalog/ingest/
# 子 agent 另做测试包编译检查，不运行用例。
go test ./internal/app ./internal/platform/postgres -run '^$' -count=1
```

在 `frontend/` 执行：

```bash
# 新增/修改的前端单测：51 PASS。
npx vitest run src/features/library/scan-status.test.ts src/features/library/api/scan-filter-decoders.test.ts src/features/library/hooks/use-root-exclusion-commands.test.tsx
# 相关 library、页面与样本合同：12 文件，813 PASS，1 个已有无关条件跳过。
npx vitest run src/features/library src/app/library*.test.tsx src/test/api-contract.test.ts
# 均 PASS。
npm run typecheck
npx eslint src/features/library/scan-status.test.ts src/features/library/api/scan-filter-decoders.test.ts src/features/library/hooks/use-root-exclusion-commands.test.tsx
npx eslint src/app/library-scans.test.tsx
```

前端唯一跳过项为既有「仓外艺人截断队列：完整 10000 项解码往返且无缺失/重复曲目」，未配置其仓外样本目录；与本项无关，不是 PostgreSQL 用例。Vitest 有 jsdom 既有 `HTMLMediaElement.pause/load` 未实现提示，退出码为 0，无失败。

修改的 Go 测试执行了 `gofmt -w`；仓库 `git diff --check` 通过。

另执行：

```bash
rg -n 'incomplete|不完整|unsupported|parse_failure|坏|损坏' frontend/e2e/real/ scripts/playback-e2e.sh
```

只有一个与本项完整性无关的播放源文件注释命中，未发现需调整的旧语义断言，无需修改 E2E 或 playback-e2e；既有 `api-mock.ts` 改动保持原样，不补额外替身。

## 遇到的旧测试失败及实现问题

- 初次单独重生 `TestPostgreSQLAPIResponseSamples` 时，旧断言要求坏 FLAC 令扫描 incomplete，实际 succeeded；符合新设计，已调整样本夹具，并与 scan-feedback 样本一起重生通过。
- 非普通音频来源的旧断言要求根不完整，实际完整；符合新设计，已改为成功跳过并保留诊断/零入库断言，精确重跑通过。
- 独立审查还发现旧 `TestPostgreSQLIncompleteRootPersistsValidCandidatesAndCleansStaging` 使用坏 MP3 制造 incomplete；改名并同步成功断言，保留原有效曲目、清理和诊断检查，精确运行 PASS。
- 未发现需要修复的实现问题，未修改实现。
- 独立审查完成，唯一旧断言遗漏已修正；审查还通过了 `go vet ./internal/library/... ./internal/app ./internal/platform/postgres`，未另跑测试。

## 验证边界与清理

仅运行上述改动直接影响的最小集合；未跑全量、Playwright、playback-e2e、真实库 smoke。浏览器与截图由主会话安排。

已实际执行并成功：

```bash
docker compose --env-file /dev/null -f "$WT/compose.test.yaml" -p roomusic-test-scan-s down -v
```

输出确认 `roomusic-test-scan-s-postgres-1` 容器和 `roomusic-test-scan-s_roomusic-test-postgres-data` 数据卷均已删除。本轮临时环境文件已移除，未操作其他 Compose 项目、容器或网络。任务维持进行中，不提交、不归档，主会话继续安排浏览器收尾。
