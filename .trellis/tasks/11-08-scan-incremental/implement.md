# ④ 增量扫描 · 实施计划

依据：[prd.md](./prd.md)、[design.md](./design.md)。S 线第 3 项，在 ① 与 ⑫ 合入 S 线分支之后开始；子 agent 不提交 git。

## 通用约束（派活时原样写进 prompt）

- 测试只做必要的：只补改动直接影响的测试，跑下面的最小相关集；不跑全量，不做演练、预演和重复验证；不为不可能的边界写测试；缓存失败按未命中处理即可，不堆额外兜底。
- 前端文案：不写防御性说明；“完整重扫”按钮与标签本身说清楚，不加解释段落、不加确认框。
- 数据库测试只用隔离的 `roomusic-test` 项目；集成测试必须 PASS 而不是 SKIP。

## 后端（GPT-6-Astra，high）

1. 迁移 `backend/migrations/0038_incremental_scan.sql` 与 `.down.sql`（design 3.9）。
2. 版本号：`parser/version.go`（`CommonVersion`、`formatVersions`、`CacheVersion`）；`catalog/ingest/version.go`（`GroupingVersion`）。
3. 观察字段：`ingest.SourceObservation` 增加 `ParserVersion`、`ArtworkDeferred`；`StageObservation` 的封面去重把 deferred 算作封面；`EmbeddedArtwork` 同步（design 3.4）。
4. 解析缓存：`library/parse_cache.go`（按目录预取、命中判断、upsert、seen 批量标记、根结束清理）；`scanner.go` 物理文件与 CUE 父音频走缓存；`full` 模式不读缓存。
5. 延迟封面：`saveArtworkForRelease` 对 deferred 观察现读封面（design 3.4）。
6. 候选跳过：`library/candidate_signature.go`（签名；封面输入取自共用的 `listFolderArtworkCandidates`，`discoverFolderArtwork` 也改为在它的结果上查找，design 3.5；O 线已合入时直接调用 ② 的列举函数）；`catalog/ingest/candidate_state.go`（`CandidateUnchanged`、`TouchTracks`、`RecordCandidateState`、清理）；`persistStagedCandidates` 接入。
7. 借用指纹：`identity_plan.go` 的 `resolveFingerprints` 前增加借用 SQL；`PlanOptions` 增加 `Mode`；`RootPlan.FingerprintsBorrowed` 与日志。
8. 完整重扫：`StartScan(ctx, StartOptions)`；`httpapi/scans.go` 解析可选请求体；`scan_runs.scan_mode` 写入与读取；presenter 增加 `mode`。
9. 日志 `library.scan.incremental_summary`（design 3.8）。
10. 合同同步（design 3.11）。

依赖：1→2→3 先做；4、6、7、8 在 3 之后可并行；5 依赖 3 与 4。

## 前端（Claude Opus 5.5，新开一个子 agent，做完即结束）

后端第 8 步的接口形状已定，可与后端并行。

1. `decoders.ts`：`ScanDTO.mode`；`endpoints.ts`：`startScan(mode?)`，`incremental` 时不发请求体。
2. 扫描区段：“完整重扫”次要按钮，与“开始扫描”共用 202/200 处理。
3. 扫描记录与详情：`mode=full` 显示“完整重扫”标签。

## 测试（GPT-6.1-Sol，与实现分开的 agent）

- 单测：`CacheVersion`（已知/未知扩展名）；候选签名对每类输入敏感（成员大小、时间、解析器版本、CUE sheet、封面候选文件、抓轨日志），对无关文件不敏感；若 O 线 ② 已合入，另验证在附属子目录 `Scans/` 新放 `box - front.png` 会改变签名。
- 集成（PostgreSQL，用解析计数桩或日志计数断言）：
  - 同一库连扫两次：第二次解析次数为 0、候选全部跳过、曲目与 Release 不变、missing 为 0、报告零变化。
  - 改一个文件的标签（mtime 变）：只重新解析该文件，只重写其候选。
  - 升 `formatVersions[".flac"]`（测试内覆盖）：FLAC 重新解析、候选重写；其他格式不解析。
  - 升 `GroupingVersion`（测试内覆盖）：全部候选重写，解析次数仍为 0。
  - 替换 `cover.jpg`、新增 EAC `.log`、修改 CUE：对应候选重写。
  - 命中缓存的带内嵌封面候选因新增一轨而重写：封面仍是内嵌封面（deferred 现读）。
  - 目录改名：曲目 UUID 不变、借用指纹（`FingerprintsBorrowed>0`、未整段读）；同名同大小同时间的两个文件 → 不借用、照常计算。
  - 完整重扫：解析全部文件、不跳过、不借用，`mode=full` 出现在 DTO；空请求体与 `{"mode":"incremental"}` 等价，非法模式 400。
  - 删除文件后：缓存项在下一次完整遍历后被清理。
- 前端（Vitest）：decoder 的 `mode`；完整重扫按钮发出的请求体。
- 已有测试：扫描生命周期与身份重绑定集成测试应不改断言即通过（R6 正确性不变的回归证据）；API 样本重生（`ScanDTO.mode`）。

### 验证命令（最小相关集）

```bash
cd backend
go test ./internal/library/parser/ -run 'CacheVersion' -count=1
go test ./internal/library/ -run 'CandidateSignature' -count=1
ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test ./internal/library/ ./internal/catalog/ingest/ -run 'Incremental|ParseCache|CandidateState|Borrow|FullRescan|IdentityRebinding|ScanLifecycle|ArtworkIntegration' -count=1
ROOMUSIC_TEST_DATABASE_URL=<隔离库> go test ./internal/platform/postgres/ -run 'TestPostgreSQLMigrationRollbackRoundTrip' -count=1
go vet ./internal/library/... ./internal/catalog/ingest/
cd ../frontend
npx vitest run src/features/library
npm run typecheck
```

### 真实库只读 smoke

在本机隔离实例上对 `music/` 连续扫两次（八号机真实库与 `music/` 的文件清单完全相同，不另在八号机跑），记录两次耗时与 `library.scan.incremental_summary` 的计数（第二次 `files_parsed=0`、`candidates_written=0`）。

## 风险文件与回滚点

- 风险文件：`library/scanner.go`（遍历回调第二次大改）、`catalog/ingest/staging.go`（封面去重）、`catalog/ingest/identity_plan.go`（借用 SQL）。
- 回滚点：迁移单独；第 2–3 步；第 4–5 步（缓存）；第 6 步（候选跳过）；第 7 步（借用）；第 8 步（完整重扫）。缓存与候选跳过可以各自关掉而不影响正确性。

## 合入前核对

- [ ] 签名输入与 design 3.5 一致；“列出封面候选文件”只有一个函数，封面查找与签名都调用它，覆盖当时 main 上的全部查找轮次。
- [ ] `observation.go` 与 O 线的字段按字母序合并（`ArtworkDeferred`、`DiscParentExclusive`、`ParserVersion`）。
- [ ] `full` 模式绕过缓存读取、候选跳过与借用，仍写缓存与状态。
- [ ] 缓存读写失败只记 WARN、按未命中处理，不改变扫描完整性。
- [ ] 已有扫描与身份集成测试不改断言即通过。
- [ ] `library-scan-contracts.md` 写明两个版本号与升版本规则（design 3.1、3.2、第 8 节表格）。
- [ ] 主会话在合入后续各线时按 design 第 8 节核对是否升了版本号。
