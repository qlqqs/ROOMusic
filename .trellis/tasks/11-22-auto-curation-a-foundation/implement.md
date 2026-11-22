# 自动整理 A：执行计划

分支：`feat/auto-curation-a-foundation`（从 `main` 切出，在主工作目录里做）。
分工按全局约定：后端实现与后端测试分别交给不同的 Codex 子代理（经 Orca 启动），实现代理不写新测试；
前端、合同文档、验收与提交由主代理做。本任务跨包并有迁移，实现用 `gpt-6.2-astra`，测试用 `gpt-6.2-astra`（并发与迁移）。
子代理不提交 Git；每一步完成后主代理审阅改动范围与结果再进入下一步。

## 步骤

### 1. 后端实现（一）：数据、三层有效值与自动层的读写

允许修改：`backend/migrations/`、`backend/internal/operations/`、`backend/internal/catalog/`（含 `governance`、`internal`、`maintenance`、`stewardtools`）、
`backend/internal/identity/`、`backend/internal/platform/postgres/` 里的回退数据。

- [x] 迁移 0058 与回退脚本、`rollbackFixtures`（design 第二节）。
- [x] `operations`：新类型常量、`Origin` 的新字段、`Spec` 的批次、`Outcome` 的“响应里的版本号”、列表按来源与批次筛选与列表项的 `batch_id`。
- [x] `catalog`：自动层的读取；待关注字段、专辑级署名、署名事实与有效署名、回填成本估算；修正状态的 `auto` 与三处预览。
- [x] `catalog/governance`：自动层的加锁、写入与派生刷新（导出给 `curation` 用），`release_auto_correction` 的 `Kind`（撤销、列表展示、撤销预览），
      写“不再提出”的注入接口。
- [x] `identity`：导出事务内的“启用状态的管理员”核对。
- 实现代理的自检：`go build ./...`、`go vet` 涉及的包、现有单元测试里受影响的包。不跑集成测试全集。

主代理审阅点：三条结构隔离（扫描、人工、自动互不读写对方的表与版本号）；视图的列与顺序不变；回退脚本的清理顺序。

### 2. 后端实现（二）：curation 包与接口

允许修改：`backend/internal/curation/`（新）、`backend/internal/app/`（装配与依赖规则表）。

- [x] 设置与核对、批次、不再提出、`Writer.Apply`、`RevertScope`（design 第三至六节）。
- [x] `curation/httpapi` 的管理员接口（design 第六、七节）；`app` 装配、`Kind` 注册、注入接口、`packageRules` 登记。
- 自检同上。

### 3. 后端测试（另一个子代理）

允许修改：各包的 `*_test.go`、`backend/internal/app/testdata/api-samples/`。发现实现缺陷时写出失败用例并报告，不改实现。

- [x] A-V1 至 A-V15、A-V17 的集成测试；接口样本：更新 `corrections/*`，新增自动整理的操作记录、批次、设置、不再提出的样本。
- [x] 现有 EXPLAIN 与读取性能测试；按来源、按批次筛选的 EXPLAIN。
- [x] 变异检查（手工）：并发（同一发行的两次自动写入、写入与撤销同时发生）、权限（开关与开启者的核对、成员被拒绝）、迁移（回退的清理）。
      做法照本机归档的 `11-05-deploy-login-throttle/research/test-agent-validation.md`：每次只改坏一处实现，确认相关用例因目标断言失败，再还原。
- 命令只覆盖本任务涉及的包与用例，用 `ROOMUSIC_TEST_RUN` 收窄，经 `./scripts/test-integration.sh` 运行（隔离的 Compose 项目与合成数据）。
  样本用 `ROOMUSIC_UPDATE_API_SAMPLES=1` 重写后人工核对差异。返工时只重跑失败的与被改动的用例。

### 4. 前端的最小适配（主代理）

- [x] `features/governance` 的解码、标签与改动摘要；`features/artists` 的名字来源；修正状态解码接受 `auto`。
- [x] Vitest：契约测试读新样本；操作记录列表显示自动整理的记录与撤销。只跑 `governance`、`artists` 与契约测试相关的用例，加类型检查。
- 不改页面结构，不需要截图复核与 Playwright。

### 5. 合同与文档（主代理）

- [x] `.trellis/spec/backend/governance-contracts.md`：自动层、三层有效值、新操作类型、来源与批次、撤销规则；新增 `curation-contracts.md`（设置、批次、撤销、不再提出的接口与错误码）并登记到 `index.md`。
- [x] `catalog-rest-contracts.md`、`artist-contracts.md`、`database-guidelines.md` 里受影响的条目。
- [ ] ADR 0006 第 13 节与路线图 M7 的 A 行补上任务与提交；`agent-and-operation-guidelines.md` 的“已决定、尚未实现”一节把本任务实现的部分改成已实现。

### 6. 收尾

- [x] 对照 A-V1 至 A-V17 逐项记录结果（本轮运行的与沿用之前结果的分开写）。
- [ ] 提交、合并回 `main`、删除任务分支、归档任务（提交与合并等用户确认）。

## 回退点

- 步骤 1 之后：有自动层但没有任何写入口，行为与现在相同。
- 视图性能不过关：把 `release_auto_values` 换成每发行一行的宽表，只影响步骤 1 的迁移与 `catalog` 的读写函数。
- 整个任务可以整体不合入：分支上的迁移没有进入任何实例。

## 启动前的检查

- [x] 用户批准本任务的规划摘要。
- [x] `implement.jsonl` 与 `check.jsonl` 已填。
