# ① 文件过滤与失败处理 · 实施计划

依据：[prd.md](./prd.md)、[design.md](./design.md)。S 线第 1 项，在 S 线 worktree 内开发；子 agent 不提交 git。

## 通用约束（派活时原样写进 prompt）

- 测试只做必要的：新增或改变的行为补对应测试，跑下面列出的最小相关集；不跑全量，不做演练、预演和重复验证；不为不可能的边界写测试；不堆防御代码和回退分支。
- 前端文案：不写防御性说明；标题、按钮、数据已说清楚的不再加说明；确实需要的说明用一句日常话。
- 数据库测试只用隔离的 `roomusic-test` Compose 项目与随机库名；集成测试必须真正运行（PASS 而不是 SKIP）。

## 后端（GPT-6-Astra，high）

1. **迁移** `backend/migrations/0034_scan_file_filter.sql` 与 `.down.sql`（design 3.9）。先查 `pg_constraint` 核对 `library_root_operations` 类型约束的实际名字。
2. **过滤与扩展名**：新建 `backend/internal/library/scan_filter.go`（`scanFilter`、内置名单、`skipDir`/`skipFile`，扩展名表从 `scanner.go` 搬入）；`loadRegisteredRoots` 一并读出排除项（单条查询），`registeredRoot` 增加 `Exclusions []string`；`countScanRoots` 与 `scanRoot` 改用 `scanFilter`。
3. **遍历中的失败分类**（`scanner.go`）：按 design 3.3 重排判断顺序（R2），各类单文件/子目录/CUE 失败改为“诊断 + `ingest.HoldSource` + 跳过计数”，不再置 `complete=false`；记 `seen`、`walkErrorSeen`、`excludedHit`；空根保护（3.4）。
4. **保住登记**：新建 `backend/internal/catalog/ingest/held_sources.go`（`HoldSource`、`TouchHeldSources`、`RootHasPresentTracks`、匹配谓词常量）；`identity_plan.go` 的 `vanishedTracksCTE` 加排除条件；`staging.go` 的两个清理函数同时删 `scan_held_sources`；`scanRoot` 在写候选之后调用 `TouchHeldSources`。
5. **封面源文件**：`discoverFolderArtwork` 错误包装为 `errArtworkSource`，`saveArtworkForRelease` 遇到时记 `artwork_unreadable`、保留原关系、不令扫描不完整；`scans.go` 的 `safeDiagnosticMessage` 增加 `root_empty`、`artwork_unreadable`，`walk_error` 文案改“文件夹读不了”。
6. **跳过计数**：`scanFeedback` 持有 `skipped`，随进度刷新与 `finalizeScanFeedback` 写 `scan_runs.skipped_files`；`Scan` 结构、`scanRow`、presenter 增加 `skipped_files`。
7. **CUE 宽松匹配**（`parser/cue_path.go`，design 3.6）：`cueNameEqual`、同名匹配、stem 回退的“恰好一个”规则；`go.mod` 把 `golang.org/x/text` 改为直接依赖。
8. **排除子文件夹接口**（`library/httpapi`）：`listRoots` 返回 `excluded_paths`；`POST/DELETE /api/v1/library-roots/{id}/exclusions`；`GET /library-folders?root_id=`（`library.ListFoldersWithin`）；注册路由。
9. **合同同步**：按 design 3.10 更新 5 个 spec 文件；每个文件不超过 24,000 字节。

2–4 有先后（同一遍历函数）；7 与 8 可与 2–6 并行。

## 前端（Claude Opus 5.5，1 个子 agent 做完全部前端）

可在后端第 1 步完成、接口合同确定后与后端并行（按 design 第 3、4 节的形状写 decoder 与 mock）。

1. `api/decoders.ts`、`endpoints.ts`：`RootDTO.excluded_paths`、`ScanDTO.skipped_files`、添加/移除排除项、目录内浏览参数。
2. `diagnostic-kinds.ts`：`root_empty`、`artwork_unreadable`，`walk_error` 改名。
3. `scan-status.ts` 与扫描摘要/详情：`incomplete` 说明改为“有目录没扫完，那里的曲目保持原样。”；成功且 `skipped_files>0` 时显示“跳过 N 个文件”，链接到该扫描的诊断。
4. 目录表：每行“不扫描的文件夹”入口、已排除列表（移除图标按钮）、“添加”打开目录内模式的 `FolderBrowser`（新增 `rootId` 属性）；`use-root-commands.ts` 增加两个命令。
5. 文案自查：无防御性说明、无多余说明。

## 测试（GPT-6.1-Sol，与实现分开的 agent）

后端实现完成后：

- 单测（新增）：`scanFilter`（隐藏段、名单大小写不敏感、整段不误伤 `my.recycle`/`recycle`、用户排除整棵子树、只看根之后的段）；`cueNameEqual` 与宽松匹配（NFC/NFD、大小写、恰好一个、多个不采用、stem 回退同规则）。
- 集成（新增，PostgreSQL）：
  - 一次扫描含 `._x.flac`、`.ape`、损坏 FLAC（库里已有曲目）、一个真正删除的文件：前三者只在诊断里，状态 `succeeded`，损坏 FLAC 对应曲目仍 present，删除的文件被标 missing，`skipped_files=2`（`._x.flac` 被过滤不计）。
  - 子目录读不了（`chmod 000`，非 root 运行）：该目录已有曲目保持 present，其他目录照常对账。
  - CUE unsafe 引用：该 CUE 已有虚拟轨保持 present。
  - 保住的曲目不被同指纹的新文件配对抢走 UUID。
  - 空根保护：已有曲目 + 空根 → `incomplete`、`root_empty`、曲目仍 present；新登记空根 → `succeeded`；排除了唯一子文件夹 → `succeeded` 且其下曲目 missing。
  - 排除项接口：幂等回放、同键异义 409、越界/等于根 400、上限 409、`excluded_paths` 返回、目录内浏览的 `parent`/`selectable`。
  - 坏 `cover.jpg`：`artwork_unreadable`，扫描 `succeeded`，原封面关系保留。
- 前端（Vitest）：decoder 新字段；跳过提示的显示条件；排除项添加/移除命令。
- 已有测试按新语义修改：`scan_rules_test.go`、`scanner_observation_test.go`、`scan_lifecycle_integration_test.go` 中“unsupported/parse failure → incomplete”的断言；API 样本重生（`ROOMUSIC_UPDATE_API_SAMPLES=1`）。另需 grep `frontend/e2e/real/` 与 `scripts/playback-e2e.sh` 中依赖“不完整”语义的地方。

### 验证命令（最小相关集）

```bash
cd backend
go test ./internal/library/ -run 'TestScanFilter|TestScannerOnlyDiagnoses|TestFileSymlinkTargets|TestCueReferenceCompletion|TestScanRoot' -count=1
go test ./internal/library/parser/ -run 'Cue' -count=1
ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test ./internal/library/ ./internal/catalog/ingest/ -run 'Held|EmptyRoot|Exclusion|ScanLifecycle|IdentityRebinding|ArtworkIntegration' -count=1
ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test ./internal/platform/postgres/ -run 'TestPostgreSQLMigrationRollbackRoundTrip' -count=1
go vet ./internal/library/... ./internal/catalog/ingest/
cd ../frontend
npx vitest run src/features/library
npm run typecheck
```

### 真实库只读 smoke

在本机隔离实例上对 `music/` 跑（八号机真实库与 `music/` 的文件清单完全相同，不另在八号机跑）：改动前与改动后各扫描一次，对比扫描状态、发行数、曲目数、missing 数、诊断类别分布、`skipped_files`。沿用 `scripts/real-library-smoke.sh` 的只读方式，不改源音乐。

## 风险文件与回滚点

- 风险文件：`library/scanner.go`（遍历回调重排）、`catalog/ingest/identity_plan.go`（vanished 排除）、`parser/cue_path.go`。
- 回滚点：迁移（第 1 步）可单独回滚；后端第 2–6 步作为一组；第 7 步、第 8 步各自独立。

## 合入前核对

- [ ] design 3.3 矩阵每一行都有代码路径对应，且“仍不完整”的情况没有被误放宽。
- [ ] 预计数与遍历只调用 `scanFilter`，没有第二套判断。
- [ ] `TouchHeldSources` 在 `persistStagedCandidates` 之后、暂存清理之前；取消时不调用。
- [ ] 五个 spec 文件已更新，单文件不超过 24,000 字节。
- [ ] 前端文案符合规则；界面截图（1440/375）由主会话复核。
- [ ] 与 M 线 ⑩ 的扩展名表冲突按 design 第 8 节处理。
