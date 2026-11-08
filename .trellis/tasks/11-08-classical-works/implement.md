# ⑧ 古典作品与乐章：实施计划

设计见 [design.md](./design.md)。T 线第 3 项，在 ⑦ 合入线分支之后开始。子 agent 不提交 git。

## 共同约束（写进每个派活 prompt）

- 前端文案：不写防御性说明，不加不必要的说明；确实需要的说明用一句日常话。作品小标题只有作品名和时长。
- 测试只做必要的：只测改动直接影响的部分，跑下面列出的最小测试集；不跑全量，不做演练、不重复验证、不为假想场景写测试、不过度兜底。
- 前后端解码器严格，`work` 字段与 `match.field` 新值前后端同批合入。

## 后端（GPT-6-Astra，high）

1. 新包 `backend/internal/catalog/classical/`（只依赖标准库）：`catalog.go`（组合子、原子解析器、卷号子解析器、内置组合、`ParseCatalogs`、`CatalogSortKey`）、`split.go`（归一化、`Segments`、公共前缀、序号反向定位、`SplitWorks`）。design §3.1–3.3。
2. `library/parser`：`AudioObservation` 加 `Work`、`MovementName`、`MovementNumber`；Vorbis/TXXX/freeform 键与 ID3 `TIT1`/`MVNM`/`MVIN`、M4A `©wrk`/`©mvn`/`©mvi`（design §3.5，含 `TIT1` 的保护条件）。
3. `catalog/ingest/observation.go` 加三个字段（JSON omitempty）；`library/scanner.go:559` 转换补三个字段（S 线共享文件）。
4. `catalog/ingest/works.go`（新文件）：`AssignWorks`；`persist.go` 在 `persistCandidate` 开头调用并传给 `persistTrack`；`persist_track.go` 的 INSERT/UPDATE 写 6 列（无作品时写 NULL）。
5. 迁移（线内草号 0035，含 `.down.sql`）：design §3.6；`rollbackFixtures` 补数据。
6. `catalog`：`projection.go` 加 `ReleaseTrackWork`，`ReleaseTrack.Work *ReleaseTrackWork \`json:"work"\``；`read.go` 读三列（`work_index` 非空才填）。
7. 搜索：`searchindex/values.go` 曲目值加 `work_title`、`work_catalog`（后者用 `textnorm.CatalogNumber`）；搜索 SQL 中编号分支改 `field IN ('catalog_number','work_catalog')`，专辑传播加这两个字段；`match.field` 新值。T 线已升过 `ProjectionVersion` 则不再升。
8. 版本（design §7）：④ 先合入 main 时，T 线 rebase 后 `parser.CommonVersion` +1、`ingest.GroupingVersion` +1（与 ⑥⑦ 合计一次）；T 线先合入则不升。
9. 合同：`library-scan-contracts.md`（作品标签与写入）、`catalog-rest-contracts.md`（`work`）、`search-contracts.md`（两个字段与传播）；新增一节“古典作品与乐章”写进 `library-scan-contracts.md`（不另建文件）。

可并行：1 独立于 2–7，可与 2、5 同时进行；4 依赖 1、3；6、7 依赖 5。

## 前端（Claude Opus 5.5，high，新开一个 agent，做完即结束）

1. `features/catalog/api/decoders.ts`：track 的 `work` 解码。
2. `features/catalog/components/release-media.tsx`：`groupTracksByWork` 纯函数 + 分组渲染（design §3.8）；`release-media.module.css` 增加作品小标题样式。
3. `features/search/api/decoders.ts` 的 `matchFields` 加 `work_title`、`work_catalog`；`match-note.ts` 的 `fieldNames` 加“作品”“作品编号”。
4. `src/test/catalog-data.ts` 等测试数据若构造 track，补 `work: null`。

## 测试（GPT-6.1-Sol，high）

- `classical`：design §5 编号表全部行；22.7.3 段切分 8 行；22.7.9 A–E、C′；§5 中文与阿拉伯补充行；`CatalogSortKey` 两例（`BWV 1043/2` 与 `BWV 1043 : 2` 等价）。
- `parser`：三种容器各一例作品/乐章读取；`TIT1` 单独出现且无编号时不读作品。
- `ingest`：`AssignWorks` 的 design §5 标签分组表与“非古典不拆”一例（纯函数）。
- 迁移：`TestPostgreSQLMigrationRollbackRoundTrip`。
- 搜索：一个 PostgreSQL 用例：`BWV 1007`、`bwv1007` 命中曲目并传播到专辑。
- API 样本：在现有样本里加一张两轨的标签作品专辑，重写样本；前端契约测试通过。
- 前端：`release-media.test.tsx` 一例：作品分组的小标题、组时长、行内显示乐章名；无作品时结构不变（已有用例保持通过即可）。

验证命令：

```bash
cd backend
go test ./internal/catalog/classical/ ./internal/library/parser/ ./internal/catalog/ingest/ -run 'Catalog|Segment|SplitWorks|Work|Movement' -count=1
ROOMUSIC_TEST_RUN='^(TestPostgreSQLMigrationRollbackRoundTrip|TestPostgreSQL.*WorkSearch.*)$' ../scripts/test-integration.sh
ROOMUSIC_UPDATE_API_SAMPLES=1 ROOMUSIC_TEST_RUN='^TestPostgreSQLAPIResponseSamples$' ../scripts/test-integration.sh
cd ../frontend && npx vitest run src/features/catalog/components/release-media.test.tsx src/features/search src/test/api-contract.test.ts && npm run typecheck
```

## 风险文件与回滚点

- 共享文件：`parser/tags.go`、`mp3.go`、`m4a.go`（O 线 ③、M 线 ⑩）；`library/scanner.go`（S 线）；`ingest/persist.go`、`persist_track.go`（O 线 ②）。
- 迁移是回滚点。

## 合入前核对

- [ ] design §5 全部行有单测；22.12 第 1–14 条各有至少一个测试覆盖（可与表格行合用）。
- [ ] 截图复核：古典专辑作品分组（1440 与 375 宽）、普通专辑不变；没有说明性文字。
- [ ] 前后端同批合入（`work` 字段与 `match.field` 新值由严格解码器校验）。
- [ ] 真实库 smoke：在本机隔离实例上对 `music/` 扫描，没有任何专辑出现作品分组（`work_source` 全为 NULL）。古典正例只靠单测。
