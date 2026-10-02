# F3 执行计划

> 需求见 [prd.md](prd.md)，设计见 [design.md](design.md)。每一步单独提交到 `main`，不推送；改动前端源码的提交同时重建并提交
> `backend/cmd/roomusic/web`。集成测试只用 `roomusic-test` compose 项目与合成数据，不碰用户自己的 `roomusic` 项目与 `music/`。

## 启动前

- [x] 用户于 2026-10-02 批准最终规划摘要，已运行 `python3 ./.trellis/scripts/task.py start .trellis/tasks/10-02-classical-f3-backend-gaps`。
- [x] 读前后端规范：`.trellis/spec/backend/{index,core0-runtime-contracts,catalog-rest-contracts,error-handling,quality-guidelines}.md`、
      `.trellis/spec/frontend/{index,state-management,component-guidelines,quality-guidelines}.md`。

## 步骤

### 第 1 步：身份接口与错误映射（R2、R4）

- [x] `identity/httpapi/handlers.go`：三处响应带 `id`；`createUser`、`setupAdmin` 只把 `23505` 映射为 409，其余 503；用户路由 ID 校验；
      撤销会话检查用户存在；`listUsers` 读行/遍历失败返回 503；12–72 字节的密码检查与新文案。
- [x] `identity/guard.go`：401 文案改为“需要登录”。
- [x] 集成测试：新增或扩展 `internal/app` 下的身份测试（触发器注入、密码边界、ID 校验、未知用户、三处 `id`）。
- [x] 重写样本：`ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh`，逐个检查 diff（只应变动 `auth/*`、`errors/unauthorized` 与新增样本）。
- [x] 前端：查看者 decoder 带 `id`、`sameViewer` 按 ID、成员“你”按 ID、账号规则 72 字节、撤销 404 文案；`api-contract.test.ts` 登记新样本；
      替身后端与 Playwright 受控后端返回带 `id` 的查看者。
- [x] 验证：

  ```bash
  cd backend && gofmt -l . && go vet ./... && go test -race ./internal/identity/... -count=1
  ./scripts/test-integration.sh
  cd frontend && npm run lint && npm run typecheck && npm run test && npm run build
  git diff --exit-code -- backend/cmd/roomusic/web   # 构建后无未提交漂移（提交前先 git add）
  ```

### 第 2 步：扫描记录（R1）

- [x] `platform/httpserver/pagination.go`：`ParsePagination`；catalog 改用它（行为不变）。
- [x] `library/scans.go`：`ListScans` 与批量诊断计数；`library/httpapi`：`GET /api/v1/scans`。
- [x] 集成测试与样本：`scans/list`、`scans/list-empty`。
- [x] 前端：`shared/ui/pagination.tsx`（含单元测试）、扫描记录的 API/decoder/查询键/hook/组件、`/library` 的 `scan_page`、失效刷新；
      Vitest 页面测试；`api-mock.ts`、`a11y.spec.ts`、`responsive.spec.ts`、`keyboard.spec.ts` 加入扫描记录。
- [x] 验证：

  ```bash
  cd backend && gofmt -l . && go vet ./... && go test -race ./internal/platform/httpserver/... ./internal/library/... ./internal/catalog/... -count=1
  ./scripts/test-integration.sh
  cd frontend && npm run lint && npm run typecheck && npm run test && npm run build && npm run test:e2e
  ```

### 第 3 步：HTTP 缓存（R5）

- [x] `platform/httpserver/frontend.go`：按 design 2.3 设置缓存头，目录路径不再列目录；`response.go`：`WriteJSON` 加 `no-store`。
- [x] `catalog/artwork`：拆出查记录与读文件；`catalog/httpapi`：封面 `ETag`、`private, no-cache`、304。
- [x] 测试：`httpserver` 单元测试（`fstest.MapFS`）、`cmd/roomusic/main_test.go` 断言内嵌 `assets/` 文件名都带哈希段、封面集成测试（200/304/401/404，
      删除文件后仍 304）。只改后端，不需要重建前端产物。
- [x] 验证：

  ```bash
  cd backend && gofmt -l . && go vet ./... && go test -race ./internal/platform/httpserver/... ./internal/catalog/... ./cmd/roomusic/... -count=1
  ./scripts/test-integration.sh
  ```

  另用 `roomusic-test` 隔离库启动真实二进制，`curl -sI` 核对 `/`、一个 `assets/` 文件、`/assets/`、`/licenses/`、一个 API 401 与封面 304
  的响应头（结果写入 `research/cache-headers-smoke.md`，只用合成数据）。

### 第 4 步：文档同步（R3、R6）

- [x] 运行合同：扫描记录接口与错误矩阵、身份错误变化、缓存策略、技术债条目（已修复的划掉，成员幂等注明“F3 裁决延到 M3”）。
- [x] Catalog REST 合同：封面缓存与 304。ADR 0001：第 6 项与决策记录。ADR 0003：决策记录加 2026-10-02 的 D1。
- [x] Core 1 路线（F3 行、扫描目录与成员矩阵行）、架构摘要与架构图（如列有修复项）、前端规范（`Pagination`、`scan_page`、查看者 ID）、
      DESIGN（扫描记录）、父任务 PRD 与 implement、M1b 的 PRD（封面缓存与分页组件已就绪）。
- [x] 改动文档的相对链接逐个检查；`git diff --check`。

### 收尾

- [x] PRD 写“实施结果与验收对照”，列实际运行的命令与结果；未运行的检查写明原因。
- [ ] `task.py archive classical-f3-backend-gaps --skip-branch-validation`（直接在 `main` 上提交，没有 PR），随后单独提交归档后的链接修正。

## 风险与回退点

- 第 1 步改动查看者 decoder：若漏改替身或受控后端，Vitest/Playwright 会因缺少 `id` 失败——这是预期的保护，不放宽 decoder。
- 第 2 步重构分页解析：catalog 的分页测试必须在改动前后都通过，证明行为不变。
- 第 3 步的 `immutable`：只对 `assets/` 生效，并由测试守住“`assets/` 下都是带哈希的文件名”。
- 每一步独立提交，可单独 `git revert`；没有迁移，不涉及数据回退。
