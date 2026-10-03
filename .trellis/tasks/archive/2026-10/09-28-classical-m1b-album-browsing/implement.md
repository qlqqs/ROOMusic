# M1b 执行计划

> 需求见 [prd.md](prd.md)，设计见 [design.md](design.md)。F3 归档后于 2026-10-02 启动。每一步单独提交到 `main`，不推送；改动前端源码的提交
> 同时重建并提交 `backend/cmd/roomusic/web`。测试与冒烟只用合成数据和 `roomusic-test` compose 项目，不读 `music/`，不碰用户自己的 `roomusic` 项目。

## 启动前

- [x] F3 已归档（用户已于 2026-10-02 批准最终规划摘要）；已运行 `python3 ./.trellis/scripts/task.py start .trellis/tasks/09-28-classical-m1b-album-browsing`。
- [x] 读规范：`.trellis/spec/frontend/` 全部层级规范、`frontend/DESIGN.md` 第 4–10 节、`.trellis/spec/backend/catalog-rest-contracts.md`。

## 步骤

### 第 1 步：接口样本与 decoder（K10、A10）

- [x] 样本测试追加双碟合成专辑、单曲专辑详情、证据、组详情、多版本组详情与两个 404；`ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh`
      重写样本，逐个核对 diff。
- [x] `features/catalog/api/decoders.ts` 增加组详情、发行版本详情、完整证据的 decoder；`api-contract.test.ts` 登记全部新样本；decoder 反例测试。
- [x] 验证：

  ```bash
  ./scripts/test-integration.sh
  cd frontend && npm run lint && npm run typecheck && npm run test
  ```

### 第 2 步：专辑列表（K1–K4、K7、K8；A1–A3）

- [x] `album-search.ts`、`/albums` 路由与 `validateSearch`、导航“专辑”、概览链接。
- [x] 端点加 `q`；`catalogKeys`；`useAlbumPage`；`Cover`、`AlbumCard`、`AlbumGrid`、`AlbumFilters`；空态、无结果、失败、刷新中。
- [x] `src/test/fake-catalog.ts` 与列表页面测试。
- [x] 验证：

  ```bash
  cd frontend && npm run lint && npm run typecheck && npm run test && npm run build
  git status --porcelain -- backend/cmd/roomusic/web   # 构建产物与源码一起提交
  ```

### 第 3 步：专辑详情（K5、K6；A4–A6）

- [x] `/albums/$groupId` 路由、`release` 参数与规范化、返回列表的 history state。
- [x] `useAlbum`、`useRelease`、`useReleaseEvidence`；`AlbumHeader`、`VersionNav`、`ReleaseMedia`、`ReleaseFacts`、`EvidenceSection`；`labels.ts`、`format.ts`。
- [x] 页面测试：单版本、多版本、版本 404、组 404、普通用户不请求证据、管理员按需请求、封面四态、1000 首曲目。
- [x] 验证同第 2 步。

### 第 4 步：浏览器检查与真实接口冒烟（A7–A9、A11）

- [x] `e2e/support/api-mock.ts` 增加 catalog 与封面路由及长内容、多介质、多版本数据；新增 `e2e/albums.spec.ts`；`a11y.spec.ts`、`responsive.spec.ts`（含字体拦截）、
      `keyboard.spec.ts` 加入新状态与路径。
- [x] 真实接口冒烟：临时脚本启动 `roomusic-test` 的 PostgreSQL 与合成素材（双碟 FLAC、生成的 PNG 封面、一张无封面专辑），走 A11 的主路径并截图；
      结果写入 `research/real-api-smoke.md`，脚本与截图留在 `/tmp`，结束后清理容器与卷。
- [x] 验证：

  ```bash
  export LD_LIBRARY_PATH=/tmp/chromelibs/root/usr/lib/x86_64-linux-gnu   # 本机无 root 时的 Chromium 系统库
  cd frontend && npm run test:e2e
  ```

### 第 5 步：文档同步（A12）

- [x] DESIGN：第 4 节 URL 形状、第 8 节落地差异（介质逐段列出、无结构树、封面无滤镜）、第 10 节 M1b 验收勾选与“仍待确认”第 2 项的状态。
- [x] 前端规范：`state-management.md`（专辑 URL 与返回列表）、`component-guidelines.md`（`Cover` 与新组件）、`directory-structure.md`（catalog 布局）、
      `quality-guidelines.md`（新增的检查）、`index.md` 状态。
- [x] Core 1 路线（M1b 行与矩阵）、架构摘要与架构图、父任务 PRD 与 implement、README（如前端检查说明有变化）。
- [x] 改动文档的相对链接逐个检查；`git diff --check`。

### 收尾

- [x] PRD 写“实施结果与验收对照”（A1–A12），列实际运行的命令与结果。
- [x] 把截图交给用户确认 K9（2026-10-03，两项都确认默认），答复已记入 PRD 的“K9 的确认”与 DESIGN 的“仍待确认”第 2 项。
- [ ] `task.py archive classical-m1b-album-browsing --skip-branch-validation`，随后单独提交归档后的链接修正。

## 风险与回退点

- 第 1 步新增合成素材可能改变既有样本（例如列表总数）：逐个核对 diff，确认只因新素材而变。
- 第 4 步的字体拦截与长内容检查可能暴露 M1a 外壳的布局问题：在本任务内修正并补回归检查，不放宽断言。
- 每一步独立提交，可单独 `git revert`；不涉及迁移与数据。
