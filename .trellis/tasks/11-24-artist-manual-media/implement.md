# 实施计划：艺人手动资料与指定对应

分支 `feat/artist-manual-media`（基于 `main`），独立工作目录 `~/workspace/.worktrees/ROOMusic/artist-manual-media`。合入本地 `main`，不推送。

## 顺序

1. 后端实现代理（gpt-6.2-astra，high）：迁移 0072、读取、五个接口、`artist_link` 操作与撤销、Wikidata 按 Q 号读取、程序层入口放宽、规范与接口样本。不写新测试；只跑编译、`go vet` 与改动包既有测试。
2. 后端测试代理（gpt-6.2-sol，high）：按 `prd.md` A1–A7 写测试（Go 单测 + 隔离 PostgreSQL 集成测试），发现实现缺陷只报告不改实现。
3. 主代理在同一分支做前端第一段（`11-24-artist-media-ui` 的 U1–U6、U9 中与手动资料有关的部分），保证合入后前端能解码新字段。
4. 主代理验收后合入 `main`。

## 验证命令（范围上限）

```bash
cd backend
mise exec -- go build ./... && mise exec -- go vet ./internal/curation/... ./internal/catalog/... ./internal/sources/wikidata/... ./internal/app/...
mise exec -- go test -race ./internal/sources/wikidata/... ./internal/catalog/artwork/...
# 隔离 PostgreSQL，按用例名收窄：
ROOMUSIC_TEST_RUN='^TestPostgreSQL(ArtistManual|ArtistLink|ArtistMedia|ArtistAPIResponseSamples$|MigrationRollbackRoundTrip$)' ../scripts/test-integration.sh
```

## 回退点

- 迁移与后端一个提交、后端测试一个提交、前端一个提交；整分支可单独回退。
