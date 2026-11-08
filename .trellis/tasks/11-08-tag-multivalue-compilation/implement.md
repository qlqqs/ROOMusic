# ⑥ 标签多值与合辑：实施计划

设计见 [design.md](./design.md)。T 线第 1 项，在 T 线 worktree 内实施；子 agent 不提交 git。

## 共同约束（写进每个派活 prompt）

- 前端文案：不写防御性说明，不加不必要的说明；确实需要的说明用一句日常话。本项前端只加一个词表项。
- 测试只做必要的：只测改动直接影响的部分，跑下面列出的最小测试集；不跑全量，不做演练、不重复验证、不为假想场景写测试、不过度兜底。
- 本项无迁移、无 REST 形状变化。

## 后端（GPT-6-Astra，high）

1. `catalog/metadata/artist.go`：拆分第 1 步（换行/NUL）与第 6 步（拉丁“姓, 名”，含团体词排除），`splitNestedArtistName` 同步；新增 `JoinArtistValues`。
2. `catalog/metadata/compilation.go`（新文件）：`IsCompilationPlaceholder`、`CompilationDisplayName`、折叠函数与占位词表。
3. `library/parser`：
   - `artist_evidence.go`：`artistTagRole` 增加专辑艺人变体键；`artistEvidenceDisplay` 不再先 `BoundedText`。
   - `tags.go`：`setAudioTag` 的专辑艺人改为分级收集、解析末尾决定；署名分支传原文；合辑键、原始年份键。
   - `mp3.go`：帧白名单加 `TXXX`、`TCMP`、`TDOR`、`TORY`；新增 `parseID3TXXX`（按规范拆描述与值，交给与 Vorbis 相同的键处理入口）；`TPE2` 全部值用 `JoinArtistValues`。
   - `m4a.go`：`cpil`；freeform 专辑艺人变体参与分级选择；`----:COMPILATION`、`----:ORIGINALDATE/ORIGINALYEAR`。
   - `parser.go`：`AudioObservation.Compilation bool`；各格式解析结束统一做“专辑艺人分级决定 + 原始年份回退”（放在新函数 `finalizeTags`，在 `applyFilenameFallback` 之前调用，避免与 O 线 ③ 改 `applyFilenameFallback` 冲突）。
4. `catalog/ingest/observation.go`：`SourceObservation.Compilation bool`（JSON `compilation,omitempty`）。`library/scanner.go:559` 的转换补这一字段（与 S 线共享文件，只加一个字段）。④ 也在 `observation.go` 加字段，合并时按字母序排。
5. `catalog/ingest/compilation.go`（新文件）：`applyCompilation`；`organizer.go` 在 `albumArtistDecision` 之后加一行调用。
6. `catalog/internal/artistgraph/facts.go`：发行级 scan 与 correction 事实遇占位词跳过；`state.go` `Version=3`。
7. `catalog/internal/searchindex/state.go`：`ProjectionVersion=2`（T 线只升一次；若 ⑦⑧⑬ 已在线分支升过则不再升）。
8. 版本（design §3.5）：T 线一起合入时合计只升一次。④ 先合入 main → T 线 rebase 后 `parser.CommonVersion` +1、`ingest.GroupingVersion` +1；T 线先合入 → 不升，由 ④ 以合入后的规则为基线。
9. 合同：`library-scan-contracts.md` 署名拆分段与错误矩阵、`artist-contracts.md`（合辑占位不建发行级事实、版本 3）、`catalog-rest-contracts.md` 一句话说明合辑发行的 `artist="群星"`。

可并行：1–2 与 3 的 TXXX 解析可同时写；5–6 依赖 2。

## 前端（Claude Opus 5.5，high）

1. `frontend/src/features/catalog/labels.ts`：`evidenceSourceLabel` 增加 `compilation: "合辑"`。

与后端并行；只此一项，做完即结束该 agent（或并入 ⑦ 的前端 agent 一次做完，见 ⑦ implement）。

## 测试（GPT-6.1-Sol，high）

单测（新增或修改，覆盖 design §5 的对照表）：

- `metadata`：`TestSplitArtistNames*` 增加换行/NUL、拉丁“姓, 名”、团体词、多逗号、样本库写法各行；`TestIsCompilationPlaceholder`（全半角、大小写、非占位）；`TestJoinArtistValues`。
- `parser`：Vorbis 专辑艺人三级回退与多值拼接；ID3 `TPE2` 多值（**修改**现有 `TestID3NullSeparatedArtistValuesPreserveEncodingAndAlbumSelection` 的期望：由只取第一个改为 `A / B`）；`TXXX:ALBUMARTIST` 回退；`TCMP`/`COMPILATION`/`cpil` 真假；原始年份只在缺年份时回退；换行原文进署名不粘连。
- `ingest`：`applyCompilation` 的 design §5 合辑表各行（纯函数，不连库）。
- `artistgraph`：一个 PostgreSQL 用例：发行 `artist="群星"` 与 correction 为占位词时不建发行级事实，曲目艺人照常。

验证命令：

```bash
cd backend
go test ./internal/catalog/metadata/ ./internal/library/parser/ ./internal/catalog/ingest/ -run 'Artist|Compilation|JoinArtist|AlbumArtist|TXXX|OriginalYear' -count=1
ROOMUSIC_TEST_RUN='^TestPostgreSQL.*Compilation' ../scripts/test-integration.sh
cd ../frontend && npx vitest run src/features/catalog && npm run typecheck
```

`TestPostgreSQLAPIResponseSamples` 不受本项影响，不跑；合辑样本不进 API 样本。

## 风险文件与回滚点

- 共享文件：`parser/tags.go`、`parser/mp3.go`、`parser/m4a.go`、`parser/parser.go`（O 线 ③、M 线 ⑩ 也改）；`ingest/organizer.go`（O 线 ②③，只加一行）；`library/scanner.go`（S 线，只加一个字段）。
- 回滚点：本项提交即一个回滚点；回退后 `artistgraph.Version` 回 2，后台按旧规则整理。

## 合入前核对

- [ ] design §5 每一行都有对应单测且通过。
- [ ] `releasesql.ArtistRefsReady` 用共享常量，`TestArtistRefsReadyUsesCurrentVersions` 通过。
- [ ] 没有升 `textnorm.Version`。
- [ ] 真实库 smoke：在本机隔离实例上对 `music/` 扫描，改动前后艺人名字集合相同（design §6.3），合辑数为 0。
- [ ] 版本按 design §3.5 的合入顺序处理（④ 先合入则 rebase 后升 `CommonVersion`、`GroupingVersion`）。
