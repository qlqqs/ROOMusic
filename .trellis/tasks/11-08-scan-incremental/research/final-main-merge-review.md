# S 线最终同步 main：合并与语义核对

日期：2026-11-08。继续既有 `git merge --no-commit --no-ff main`；不提交、不启动数据库、不连接八号机或生产、不新增测试。备份分支保持 `scan-s-pipeline-pre-merge-main-2`。

## 冲突解决

| 文件 | 处理 |
| --- | --- |
| `backend/cmd/roomusic/web/index.html`、`assets/*` | 从合并后的前端源码统一重建，移除旧哈希产物，保留新的 CSS/JS 与入口 |
| `backend/internal/app/dependency_rules_test.go` | ingest 同时允许 T 的 `catalog/classical` 和 S 的 `platform/workpool`；其余自动合并规则保留 |
| `backend/internal/catalog/ingest/observation.go` | S 的延迟封面、封面来源、解析器版本、文件状态与 T 的 Compilation、Work、MovementName、MovementNumber 取并集，字段不重复 |
| `backend/internal/library/scanner.go` | 物理观察同时传递 T 的合辑/作品字段与 S 的 ArtworkRelativePath、ParserVersion；CUE 已自动合并的作品与父解析版本字段保留 |
| `backend/internal/platform/postgres/migrations_rollback_integration_test.go` | 保留 T 38/39/40 与 S 41/42 全部夹具、旧版种子和断言 |

## 迁移改号

- `backend/migrations/0041_incremental_scan.sql`、`0041_incremental_scan.down.sql`：④最终编号。
- `backend/migrations/0042_scheduled_scan.sql`、`0042_scheduled_scan.down.sql`：⑪最终编号。
- main 的 `0038_credit_roles`、`0039_classical_works`、`0040_search_punctuation` 不动。
- SQL 内容未改，0001–0042 连续；按现行回退政策，0031–0042 上下行成对；回退测试中 S 的 fixture 键、版本分支与断言文字改为 41/42，38/39 只用于 T 的署名/作品迁移。

同步编号引用的文件：

- `backend/internal/platform/postgres/migrations_rollback_integration_test.go`
- `.trellis/spec/backend/library-scan-contracts.md`
- `.trellis/spec/backend/scan-coordination-contracts.md`
- `.trellis/tasks/11-08-scan-incremental/design.md`
- `.trellis/tasks/11-08-scan-incremental/implement.md`
- `.trellis/tasks/11-08-scan-incremental/research/merge-main-integration.md`
- `.trellis/tasks/11-08-scan-incremental/research/part1-test-report.md`
- `.trellis/tasks/11-08-scheduled-scan/design.md`
- `.trellis/tasks/11-08-scheduled-scan/implement.md`

## 3a：候选输入覆盖

原候选签名仅含文件状态与版本。本次补充 `ingest.CandidateMetadataSignature`，library 在既有签名增加 `d` 行。对完整候选 JSON 取确定性摘要，保留碟轨和证据顺序，不只逐个手写 T 字段白名单。

| T 新增或改变语义的输入 | 是否进入签名 |
| --- | --- |
| `AudioObservation.Compilation` → `SourceObservation.Compilation` | 是，成员观察 JSON |
| 多值 `Artist`、`AlbumArtist` 与专辑艺人回退 | 是，观察及最终候选艺人、字段决定 |
| `ArtistTagEvidence[].Role/TagKey/RawValue/SourceKind/OccurrenceIndex`，含新署名键、原文和出现顺序 | 是，完整数组 |
| `Credits[].Role/Name/Detail/Source`；新增 lyricist/arranger、乐器细分、tag/lyrics 来源 | 是，完整数组 |
| `Work`、`MovementName`、`MovementNumber` | 是，三个新增观察字段 |
| 原始日期/年份回退 | 最终 `Year` 进入；私有 `originalYears` 只是解析临时输入，不直接写库 |
| 歌词头部署名、`Lyrics`、`LyricsSource` | 是，歌词、来源与派生的 Credits 均覆盖 |
| 私有 `workTags` | 不直接存库；finalize 后的 Work/MovementName/MovementNumber 已覆盖 |
| 作品归组的 `Title`、候选 `Genres`、`Mediums` 的碟/轨序与 Position/Disc | 是；成员增删、顺序变化均使摘要变化 |
| ⑥合辑派生的候选 Artist/AlbumArtist、Decisions 与既有其它候选字段 | 是 |
| ⑬ `loose_value/loose_grams` | 非 observation/候选输入，由搜索与艺人投影版本维护；对应原文输入已覆盖 |

`RawAtoms` 不传入 SourceObservation、不用于候选写库，因此无需独立摘要。封面字节与 `ArtworkDeferred` 是同一封面的两种加载形式，归一后不参与元数据摘要；文件大小/mtime、封面来源路径/MIME、外部封面候选仍覆盖。`JoinGroupID` 仅用于新建发行，排除以免首次保存与沿用时签名变化；身份/归属仍由 `CandidateUnchanged` 校验。内容指纹仍由身份规划处理，文件状态仍由 library 签名处理。无新增版本常量。

## 3b：版本与首扫

代码事实与“main 已 bump CommonVersion”的前提不同：`git ls-tree main backend/internal/library/parser/version.go` 无输出。main 不含④缓存与候选状态；T 设计明确 T 先合入则由④以合入后的规则为版本基线。

- 遵守④ PRD 裁决：`CommonVersion=1`、各格式版本=1、`GroupingVersion=1`，CacheVersion 为 `c1.f1`，不再 bump。
- 从 main 升级：0041 新建空缓存/候选状态，首扫完整解析并写入 T 新事实，不靠投影版本让缓存失效。
- 后续 parser 版本变化：缓存命中比较 `entry.version == parser.CacheVersion(ext)`；物理/CUE 父音频版本传入 SourceObservation，进入候选签名，确保重新解析后重写。
- `artistgraph.Version=4`、`searchindex.ProjectionVersion=2` 保留；各自后台回填，不参与 parser 缓存判定。
- 若复用合并前 S 试验分支的版本 1 缓存，并不能声称它会自动重新解析；测试应使用新隔离库或完整重扫。这不是 main 升级路径，不擅自改版本或添加清缓存迁移。

## 3c：并行解析与 OGG

- ⑤实际设计路径为 `11-08-scan-concurrency/design.md`，没有 `scan-pipeline*` 任务目录。
- `scan_pipeline.submit` 的未命中音频和 CUE 父音频统一调用 `parser.ParseAudioFile`；应用/缓存写入仍在遍历 goroutine 按序进行。
- FLAC、MP3、OGG、Opus、WAV、M4A、APE、WavPack、AIFF/AIF 成功路径均在文件名兜底前执行 `finalizeTags`。缓存保存的是 finalization 后的结果，命中不再重复 finalization。
- OGG/Opus 保留 S 的逐页/逐段读取、跨页拼包和流式音频哈希；完整 Vorbis comment 与 OpusTags 包仍调用 `parseVorbisComments` → `setAudioTag`，所有 T 新标签读取沿用共用入口，随后执行作品、歌词署名、专辑艺人、年份收口。
- 保留⑤既有 `DurationSeconds=0` 合同，没有按初稿新增 granule 时长。

指定测试首次发现既有 `TestOggOpusObservationsMatchHEADBaseline` 不兼容：T 规范化后的空 Credits 为 `[]`，旧基线为 null；反射逐字段还会读取 T 新增私有临时字段而 panic。仅修改此既有测试：四项基线空 Credits 对齐，跳过非公开字段（JSON 本来不含它们），全部公开观察字段和独立指纹仍比较；未新增测试。

## 3d：作品与跳过

`AssignWorks` 仍在 `PersistCandidateWithFeedback` 开始处按碟轨序计算。其输入（作品/乐章标签、标题、流派、碟轨次序）全部进入摘要；变化即重写并替换作品列。签名相同且身份/归属检查通过时仅 TouchTracks 更新 observed_at，不清空作品列，不因跳过丢作品。main 升级首次无状态可跳过，因此已有作品空列也会在首扫写入。

CUE 不继承父音频 Credits/Compilation 是 main 已记录的既有边界；本次保留，没有扩展继承规则。

## 3e：调度器

调度器源码与 T 无交集；0042 改号及回退夹具对齐，应用整体编译通过。不启动服务、不触发定时扫描。

## 验证与范围

实际执行与结果：

| 命令 | 结果 |
| --- | --- |
| `gofmt -l`（`git diff --name-only --diff-filter=ACMR HEAD -- '*.go'` 得到的85个文件） | 无输出，通过 |
| `cd backend && go build ./...` | 通过 |
| `cd backend && go vet ./internal/...` | 通过 |
| `cd backend && env -u ROOMUSIC_TEST_DATABASE_URL go test ./internal/library/... ./internal/catalog/... ./internal/app -run 'Test[^P]' -count=1` | 首次 OGG 既有测试不兼容；适配后重跑通过 |
| `cd backend && env -u ROOMUSIC_TEST_DATABASE_URL go test ./internal/library/parser -run TestOggOpusObservationsMatchHEADBaseline -count=1` | 适配后的最小复测通过 |
| `cd backend && GOPROXY=off GOSUMDB=off go vet ./internal/library/... ./internal/catalog/ingest/`（复核 agent） | 通过 |
| `cd frontend && npm run build && npm run typecheck` | 通过；复用现有依赖，无需 npm ci；PostCSS 有非阻断 from 参数警告 |
| `git diff --check`、`git diff --cached --check` | 通过 |
| `git ls-files -u`、`git rev-parse -q --verify MERGE_HEAD` | 无未合并条目；合并仍进行中、未提交 |

另以 Python 校验迁移连续性、0031起的 down 配对、改号前后 SQL 字节相同，并全仓搜索旧迁移文件名及旧 S fixture 引用，无残留。校验脚本初次误要求历史0001起全部有 down，随后按项目0031起要求修正；未修改历史迁移。

数据库变量显式移除，集成测试跳过不能算数据库验证通过；数据库往返及新增回归由另一个测试会话处理。新增摘要仍需测试 agent 覆盖 T 字段敏感性、作品次序/流派、缓存与非缓存封面等价、不修改原候选、JoinGroupID 排除。未跑全量测试，使用用户指定最小包集。

范围外提示：`library-scan-contracts.md` 在两侧原分支均已超过 spec 的 24 KB 限制（main 约72 KB，S约79 KB）；本次只同步合并合同和编号，不擅自进行大规模拆文档。
