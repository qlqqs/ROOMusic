# S 线同步 main：冲突整合记录

日期：2026-11-08。合并前备份：`scan-s-pipeline-pre-merge-main`。

## 当前迁移编号（2026-11-08 再次同步 main）

本次 main 已含 0038 署名角色、0039 古典作品与 0040 搜索忽略标点；增量扫描改为 0041，定时扫描改为 0042（均含 `.down.sql`），迁移从 0001 连续到 0042。回退测试保留 main 的 38/39/40 夹具及旧数据种子，并把 S 的增量、定时夹具与对应分支/断言改为 41/42。SQL 内容不变，未新增测试、未启动数据库，历史 PASS 不代表此次合并已通过集成验证。

以下为此前同步 M/O 线的历史记录；其中线内编号 0038、缺号失败和验证结果均指当时集合，不作为当前编号或验收结论。文件路径已指向现编号。

## 范围与状态（此前同步）

只整合 S 的④第一部分、⑤与 main 的 M/O 已有规则，不实施④第 6 步候选跳过，不改变产品规则。保留当前 merge，不提交、不 abort/reset、不切换任务指针、不动其他 worktree。数据库验收留给后续 agent。

## 六处冲突

| 文件 | 处理 |
| --- | --- |
| `.trellis/spec/backend/library-scan-contracts.md` | 原地保留两边章节；按碟目录/文件名/收藏、格式、并发/缓存主题排列；同步首发版本与迁移引用，不拆文件。 |
| `backend/internal/app/app.go` | 同时保留 `WithScanConcurrency(settings.ScanWorkers)`、个人服务构造及收藏转移注入。 |
| `backend/internal/library/service.go` | 同时保留 `scanConcurrency` 和 `albumFavoriteMover`。 |
| `backend/internal/catalog/ingest/observation.go` | 47 个字段按字母序合并；程序比对双方字段类型与 JSON 标签完全保留，包括原有 `omitempty`。 |
| `backend/internal/library/scanner.go` | `persistOneCandidate` 同时接收增量统计并返回 `(releaseID,error)`；保留有序槽位、CUE 对齐后碟父目录标记、版本组规划、封面过滤、held 处理和根保存后收藏转移；不另建扩展名表。 |
| `backend/internal/platform/postgres/migrations_rollback_integration_test.go` | 保留 main 的 36/37 夹具，增量夹具、升级前种子条件与 full 模式断言改为 38。 |

## 迁移改号与版本

- 原编号 0036 的上行迁移，经线内 0038，现为 `backend/migrations/0041_incremental_scan.sql`。
- 原编号 0036 的下行迁移，经线内 0038，现为 `backend/migrations/0041_incremental_scan.down.sql`。
- 上下行 SQL 只改文件名，已与 HEAD 逐字节核对一致。
- 引用同步：上述回滚测试、`library-scan-contracts.md`、`scan-coordination-contracts.md`、本任务 `design.md`、`implement.md`、`research/part1-test-report.md`；历史 PASS 明确是改号前结果，不冒充当前验证。
- 使用线内连续编号 0038；若 T 线先合入 main，由主会话统一改号。未补空迁移，未改迁移加载器或回退执行器。
- `CommonVersion=1`、`GroupingVersion=1`；原格式及新增 `.ape/.wv/.aif/.aiff` 的格式版本均为 1，`CacheVersion` 返回 `c1.f1`。新增格式覆盖已知扩展名及大小写版本单测。
- `artistgraph.Version=3`，沿用 main，不追加升版。

## 交叉检查

1. **文件名推断不冻结**：`parseCache.write` 在有序应用时只保存 `parser.AudioObservation`；`scanPipeline.apply/applyCue` 对命中项仍构造并暂存观察。`OrganizeObservations` 每轮执行 CUE 合并、专辑碟号预处理、`prepareFilenameEvidence`，轨号/碟号/艺人均在此重推断；不把 organizer 结果写回解析缓存。
2. **碟目录标记包含缓存观察**：槽位 `Drain` 完成后，执行 `AlignCueStagingScopes` → `MarkDiscParentScopes` → `PlanStagedRoot`。标记使用本次暂存及 held 记录，不因缓存命中跳过。
3. **附属目录封面仍重新查找**：④第 6 步未接入，所有候选仍保存；无内嵌封面时调用带 `root.filter` 的四轮发现，覆盖附属目录；deferred 内嵌封面仍现读且优先，没有字节再找文件夹封面。
4. **并发不改变 O 写入顺序**：worker 只解析/计算指纹；槽位按遍历次序在 holder 应用，排空后规划、逐候选短事务保存，版本组目标按首个成功发行确定，全部候选处理后才转移收藏。取消检查及转移 WARN 语义保留。

这些结论为代码语义审查；数据库路径本轮未实跑，不能替代后续集成验收。

## 实际验证

除注明外，Go 命令在 `backend/` 执行：

| 命令 | 结果 |
| --- | --- |
| `gofmt -l backend`（仓库根） | PASS，无输出。 |
| `go build ./...` | PASS。 |
| `go vet ./internal/...` | PASS。 |
| `env -u ROOMUSIC_TEST_DATABASE_URL go test -race ./internal/library/... ./internal/catalog/ingest/... ./internal/platform/... -count=1` | 首次因跨号方案导致 postgres 四项迁移集合测试失败；改为连续编号后的重跑结果见下文。 |
| `env -u ROOMUSIC_TEST_DATABASE_URL go test ./internal/app -run '^TestPackageDependencyDirections$' -count=1` | PASS。 |
| `git diff --check`（仓库根） | PASS。 |
| `cd frontend && npm run typecheck`（仓库根） | PASS。 |

首次整合未运行全量测试、未启动数据库，当时按要求未运行 `npm run build`，嵌入资源保持 main 合并带入的原样；后续用户明确要求构建并保留产物，结果见下文。按用户要求只运行指定最小门禁；数据库相关测试未配置连接，能进入数据库检查的用例跳过，不算集成通过。

### 首次跨号方案的失败记录（已改为连续编号）

`loadMigrations`（`backend/internal/platform/postgres/migrate.go`）要求从 1 开始连续编号，首次跨号方案的 37 → 41 缺口导致 `missing migration version 38 before 41`。因此不只是回滚测试，当时集合的正向迁移加载也受阻。下列四项失败：

- `TestEmbeddedMigrationsIncludeCatalogSearchAfterScanFeedback`
- `TestLoadMigrationsRollbackValidation`
- `TestPostgreSQLMigrationRollbackRoundTrip`
- `TestEmbeddedMigrationRollbackFiles`

回退规划同样检查连续性，理由为 `not_contiguous`。未通过补空迁移、跳过断言或放宽规则掩盖失败；用户随后裁决改用线内连续编号 0038；合入 main 时若 T 线已先合入，再统一改号。

## 独立审查

`trellis-check` 未发现冲突整合新增的明确语义问题，当时确认跨号方案的迁移缺号是阻断。审查未编辑文件、未运行测试或连接数据库；另外执行了以下窄范围静态命令，均通过：

```bash
cd backend
go vet ./internal/library/... ./internal/catalog/ingest/ ./internal/app/ ./internal/platform/postgres/
go build ./internal/library/... ./internal/catalog/ingest/ ./internal/app/ ./internal/platform/postgres/
```

最终 `git ls-files -u` 为空，冲突解决已暂存；`git diff --check` 与 `git diff --cached --check` 均通过，增量迁移旧文件名前缀检索无残留引用。未调用会自动提交的 Trellis 归档/会话记录脚本；④第 6 步和数据库验收仍未完成，任务不归档。


## 连续改号与前端产物补齐验证（2026-11-08）

- 用户裁决：使用线内连续编号 0038；若 T 线先合入 main，由主会话再次统一改号。迁移上下行内容不变，回滚夹具、种子条件、模式断言与所有当前文档引用已同步。旧文件名前缀检索无结果，正向迁移连续为 0001–0038。
- 指定命令重跑 PASS（所有列出的包通过，postgres 不再有缺号失败）：
  ```bash
  cd backend
  env -u ROOMUSIC_TEST_DATABASE_URL go test -race ./internal/library/... ./internal/catalog/ingest/... ./internal/platform/... -count=1
  ```
- 不启动数据库；数据库集成用例因未配置连接而跳过，本次结果不替代数据库往返验收。不运行全量测试。
- 独立 `trellis-check` 只读审查通过：SQL 内容未改，36/37 未误改，38 夹具/分支/断言一致，文档的旧失败明确标为历史。未额外运行 build/vet/test。
- 用户追加要求后执行 `cd frontend && npm run build`，PASS（包含 `tsc --noEmit` 与 Vite 构建）。出现非阻断 PostCSS `from` 选项警告，不影响退出码。
- **产物有变化且保留**：相对构建前，入口 JS 从 `index-DiHaZzr2.js` 变为 `index-B1IB4Jxj.js`，CSS 从 `index-l_WFDUDu.css` 变为 `index-CGHoJG89.css`，`backend/cmd/roomusic/web/index.html` 同步引用；其余构建文件内容未变。没有还原产物。
- `git diff --check`、`git diff --cached --check` 均通过。所有本轮修改已暂存，保留未提交的 merge，不归档任务。
