# 实施计划：网络搜索

分支 `feat/artist-web-search-model`（基于合入手动子任务后的 `main`），独立工作目录 `~/workspace/.worktrees/ROOMusic/artist-web-search-model`。合入本地 `main`，不推送。

## 顺序

1. 后端实现代理（gpt-6.2-astra，high），分两段提交以便审阅：
   - 段一：迁移 0073、来源等级与替换、依据泛化、`web_search` 来源与依赖状态、插件按名字查、待定项两种目标（领取、计数、到期、收尾）、例外读取。
   - 段二：两个模型工具、审查证据、Escalate、例外采纳/不采纳与 `superseded`、状态接口、后台联网工具、系统提示、mock、规范与接口样本。
   - 不写新测试；只跑编译、`go vet` 与改动包既有测试。
2. 后端测试代理（gpt-6.2-sol，high）：按 `prd.md` B1–B9 写测试；mock 三条路用集成测试跑通。
3. 主代理在同一分支做前端第二段（`11-24-artist-media-ui` 中网络搜索、候选、设置行、例外列表）。
4. 主代理验收：隔离 PostgreSQL + mock + 后端二进制，合成一位无对应的艺人，看三条路与界面；合入 `main`。

## 验证命令（范围上限）

```bash
cd backend
mise exec -- go build ./... && mise exec -- go vet ./internal/curation/... ./internal/catalog/... ./internal/app/... ./internal/upstreammock/... ./internal/plugins/... ./internal/steward/...
mise exec -- go test -race ./internal/upstreammock/... ./internal/plugins/... ./internal/curation/stewardtools/...
ROOMUSIC_TEST_RUN='^TestPostgreSQL(ArtistWebSearch|ArtistMedia|CurationModel|CurationException|ArtistAPIResponseSamples$|MigrationRollbackRoundTrip$)' ../scripts/test-integration.sh
```

## 回退点

- 段一、段二、测试、前端各一个提交；整分支可单独回退。
