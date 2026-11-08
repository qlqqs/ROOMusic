# ⑬ 搜索忽略标点：实施计划

设计见 [design.md](./design.md)。T 线第 4 项，在 ⑧ 合入线分支之后开始。子 agent 不提交 git。本项无前端改动。

## 共同约束（写进每个派活 prompt）

- 测试只做必要的：只测改动直接影响的部分，跑下面列出的最小测试集；不跑全量，不做演练、不重复验证、不为假想场景写测试、不过度兜底。
- 不升 `textnorm.Version`，不改 `Text`、`CatalogNumber`。

## 后端（GPT-6-Astra，high）

1. `catalog/textnorm/textnorm.go`：`Loose`（design §3.1）。
2. 迁移（线内草号 0036，含 `.down.sql`）：design §3.4；需要时在 `rollbackFixtures` 加空条目。
3. `catalog/internal/searchindex`：`normalizeValues` 计算 `Loose` 与 `loose_grams`；单对象与批量写入 SQL 写两列。
4. `catalog/internal/artistgraph/profiles.go`：搜索名字同样写两列。
5. 搜索 SQL（`catalog/search_ranked_sql.go`、`search_tiers_sql.go`、`search_probe_sql.go`、`search_sql.go`、`search_artists.go`、`search_aliases.go`、`search_complete.go` 中涉及匹配与等级的部分）：L1、L2 分支、启用条件、六层排名（design §3.3）。参数仍全部绑定，不拼接用户输入。
6. 版本：T 线已升过 `ProjectionVersion`、`artistgraph.Version` 则不再升；单独合入时各 +1。
7. 合同：`search-contracts.md`（规范化一节加去标点键、查询语义一节加三分支与六层、迁移一节加 0036）。

1、2 可并行；3、4 依赖 1、2；5 依赖 3。

## 测试（GPT-6.1-Sol，high）

- `textnorm`：design §5 键生成表全部行（纯函数）。
- `searchindex`：`loose_value` 为 NULL 与非 NULL 各一例（纯函数 `normalizeValues`）。
- PostgreSQL：一个用例覆盖 design §5 查询表的 `acdc`、`ac/dc`、`guns n roses` 两种撇号、`夜曲`、`c#` 各行的命中与顺序；一个艺人搜索用例（`ac/dc` 艺人名）。
- 回填：一个用例：升级版本后后台回填完成、`loose_value` 写入、发布后 `acdc` 命中（沿用现有维护测试的夹具）。
- 迁移：`TestPostgreSQLMigrationRollbackRoundTrip`。
- 性能：只跑一次 I10 热门词复测（“周杰伦”“周杰倫”建议与三栏首屏，5 次预热/30 次测量，p95 ≤ 1 秒）；不跑回填耗时与其它规模测量。

验证命令：

```bash
cd backend
go test ./internal/catalog/textnorm/ ./internal/catalog/internal/searchindex/ -run 'Loose|NormalizeValues' -count=1
ROOMUSIC_TEST_RUN='^(TestPostgreSQLMigrationRollbackRoundTrip|TestPostgreSQL.*Punctuation.*|TestPostgreSQL.*LooseBackfill.*)$' ../scripts/test-integration.sh
# I10 复测：按 artist-contracts.md §6 现有基准的“仅热门词”方式执行一次，命令以现有基准测试为准
```

## 风险文件与回滚点

- 共享：无跨线文件（搜索 SQL 只有 T 线改；S 线不改搜索）。线内与 ⑧ 共改 `searchindex/values.go` 与搜索 SQL 的编号分支，⑧ 先合入线分支。
- 迁移是回滚点。

## 合入前核对

- [ ] design §5 两张表全部有测试且通过。
- [ ] I10 热门词 p95 ≤ 1 秒（一次正式测量）。
- [ ] `textnorm.Version` 未变，艺人名字键数量在回填前后不变（集成测试里断言）。
