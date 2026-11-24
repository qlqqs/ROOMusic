# 实施计划：艺人图片与简介

分支 `feat/m8-artist-info`（基于合入 mock 服务器之后的 `main`），独立工作目录 `~/workspace/.worktrees/ROOMusic/m8-artist-info`。合入本地 `main`，不推送。

## 顺序

1. 后端实现代理（gpt-6.2-astra，high）：
   - `sources` 新能力、中立类型、窄接口；MusicBrainz url-rels；五个新源子包（Fanart.tv 的内置项目密钥用 ldflags 变量，同 AcoustID）。
   - 迁移、`curation` 写入与撤销、程序层 subject、`catalog/artwork` 引用、读取接口与样本。
   - app 组装（注册源、操作类型、闸门、mock 地址）；`deploy/production/Dockerfile` 与 `.github/workflows/release.yml` 加可选的 `fanarttv_project` secret（缺了只警告）。
   - mock 服务器加五个来源。
   - 规范：`sources-contracts.md`（五个源小节、请求表、发给外部的数据）、`curation-contracts.md`（新的一节）、`artist-contracts.md`（读取字段）。
   - 不写新测试；只跑编译、`go vet` 与改动包的既有测试。
2. 后端测试代理（gpt-6.2-sol，high）：各源客户端（受控本地服务）、优先级与回退、写入的跳过、撤销与抑制、闸门、迁移往返、读取样本与重定向、mock 路由。
3. 主代理：前端（艺人页、列表、外部源卡片、治理），前端测试与 Playwright 冒烟；390 与 1440 截图核对。
4. 主代理验收：开发后端接 mock，打开自动整理，看艺人页；切 Fanart.tv 到 `unavailable` 看回退。

## 验证命令

```bash
cd backend
mise exec -- go build ./... && mise exec -- go vet ./internal/sources/... ./internal/curation/... ./internal/catalog/... ./internal/upstreammock/... ./internal/app/...
mise exec -- go test -race ./internal/sources/... ./internal/upstreammock/...
# 需要 PostgreSQL 的 curation、catalog、app 用例按 docs/development-environment.md 的隔离做法，只跑相关包与 -run
cd ../frontend
mise exec -- npm run check     # 以 package.json 实际脚本为准：类型检查、lint、vitest 的相关文件
mise exec -- npx playwright test e2e/artists*.spec.ts e2e/curation*.spec.ts
```

## 回退点

- 迁移、后端、前端分别提交；前端可单独回退（后端新字段为可选）。
