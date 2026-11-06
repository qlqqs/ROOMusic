# 调研：管理界面“备份”分区与版本号显示的前端现状

- **问题**：管理员页面与导航、"状态 + 立即执行 + 列表"的组件模式、文件下载先例、版本号显示、文案与测试约定、构建产物嵌入
- **范围**：仓库内部（`frontend/`、`.trellis/spec/frontend/`、`frontend/DESIGN*.md`）
- **日期**：2026-11-06
- 只记录现状，不含实现建议。下文路径都相对于仓库根 `/home/qlqq/workspace/ROOMusic`。

## 1. 管理员页面：路由、页面组件、导航入口

### 路由（`frontend/src/app/router.tsx`）

- 所有页面挂在 `authenticatedRoute` 下（`router.tsx:68-80`），路由不使用 loader（`router.tsx:35` 注释）。
- 管理页统一在路由组件里包一层 `<AdminGate>`（`router.tsx:238-246` 注释写明"管理页由 AdminGate 把关"）：
  - `/library` → `LibraryPage`，带 `scan_page` 查询参数（`router.tsx:239-256`）
  - `/library/scans/$scanId` → `ScanDetail`（`router.tsx:317-331`）
  - `/members` → `MembersPage`（`router.tsx:258-266`）
  - `/operations`（`router.tsx:269-283`）、`/issues`（`router.tsx:286-299`）、`/claims`（`router.tsx:302-315`）、`/artist-candidates`（`router.tsx:219-236`）
- 路由树的组装在 `router.tsx:336`（一行 `addChildren([...])`），新增路由要同时加进这里。
- `AdminGate`：`frontend/src/app/routes/admin-gate.tsx:9-12`，非管理员渲染 `ForbiddenPage`，子内容不渲染，所以不会调用管理接口。

### 导航（`frontend/src/app/shell/nav-items.ts`）

- `NavItem.to` 是一个字面量联合类型（`nav-items.ts:5`），新增入口要扩这个类型。
- 导航项列表 `nav-items.ts:17-30`，两组：
  - "聆听"（所有人）：概览、专辑、艺人、我的音乐、正在播放
  - "资料库"（`adminOnly: true`）：扫描目录 `/library`、元数据问题 `/issues`、待认领 `/claims`、可能是同一位 `/artist-candidates`、成员 `/members`、操作记录 `/operations`
- 目前没有"系统 / 设置 / 维护"之类的第三组；分组由 `group` 字段字符串决定，`visibleNavGroups` 按首次出现顺序归并（`nav-items.ts:38-48`），所以加一个新的 `group` 值就会多出一组小标题。
- 图标来自 `lucide-react`（`nav-items.ts:1`）。
- 导航渲染：`frontend/src/app/shell/primary-nav.tsx:8-31`；侧栏与窄屏菜单各渲染一次（`frontend/src/app/shell/app-shell.tsx:115-122`、`127-139`）。
- 测试固定了完整的导航列表：`frontend/src/app/shell/nav-items.test.ts:5-16`（普通成员 / 管理员 / 艺人不可用三种情况），改导航必须同步改这里。
- `frontend/DESIGN.md:187`："左侧按'聆听 / 资料库'组织入口……只有真实可用的页面进入主导航。管理员管理功能与普通浏览分层"。

### 现有管理页的分区

- 扫描目录页 `frontend/src/app/routes/library-page.tsx:8-17`：`<Page title="扫描目录">` 下依次是
  `ScanSection`（"扫描"）→ `RootSection`（音乐目录）→ `ScanHistorySection`（"扫描记录"）→ `RootOperationsSection`（"目录操作记录"）。
- 成员页 `frontend/src/app/routes/members-page.tsx:5-11`：`<Page title="成员">` 包 `MemberManagement`，其中有"新建成员""成员列表"两个 `Panel`（`frontend/src/features/members/components/member-management.tsx:55-77`）。
- 概览页 `frontend/src/app/routes/overview-page.tsx:10-29`：管理员额外看到 `ScanSummary`（带"扫描目录"链接）、`RootSummary`、`MemberSummary`；`ScanSummary` 用 `Panel` 的 `action` 放跳转链接（`frontend/src/features/library/components/scan-summary.tsx:10-18`）。

### 新增分区可以挂的位置（只列现有结构里有的位置）

- 新顶层页：`router.tsx` 加一个包 `AdminGate` 的路由，加进 `router.tsx:336` 的路由树；`nav-items.ts` 加一项（扩 `to` 类型）；同步改 `nav-items.test.ts`。可以放进现有"资料库"组，也可以用新的 `group` 值单独成组。
- 现有页加 `Panel`：`LibraryPage`、`MembersPage` 都是"`Page` + 若干 `Panel`"的组合，加一个 `<Panel title="备份">` 不需要改路由和导航。
- 概览摘要：管理员专用摘要块的写法见 `overview-page.tsx:24-25`（`isAdmin ? <X /> : null`），配合 `Panel action={<Link …/>}` 链到详情页。
- 目录结构上，新 feature 放在 `frontend/src/features/<name>/`，子目录为 `api/ components/ hooks/ query-keys.ts index.ts`（`.trellis/spec/frontend/directory-structure.md:46-57`）；`index.ts` 只导出 app 组合页面需要的内容（例如 `frontend/src/features/library/index.ts:1-9`）。

## 2. "状态 + 立即执行按钮 + 列表"的现有模式

### 典型：扫描区段 `frontend/src/features/library/components/scan-section.tsx`

- 结构（`scan-section.tsx:15-66`）：
  - `Panel title="扫描"` 包 `QueryContent`（加载用 `Skeleton`）
  - 空闲时一句状态 `<p>没有正在运行的扫描。</p>` 加主按钮 `<Button variant="primary" icon={<Play/>} busy={start.isPending}>`，文案在忙时换成 "正在开始…"（`scan-section.tsx:34-41`）
  - 命令失败：`<FailureNotice error context="write" messages={scanFailureMessages} action={<Button onClick={begin}>重试</Button>} />`（`scan-section.tsx:47-54`）
  - 按状态码区分结果：202 新建后跳详情，200（已有在跑）显示 `<Notice tone="info">` 带链接（`scan-section.tsx:21-27`、`55-63`）
  - 回调先检查 `scope.isCurrent()`，避免会话切换后的旧回调（`scan-section.tsx:24`）
- 列表：`ScanHistorySection`（`frontend/src/features/library/components/scan-history-section.tsx:41-82`）
  - `DataTable` + `Column<T>[]`（`scan-history-section.tsx:13-34`），时间列 `formatDateTime`，状态列 `StatusTag`，数字列 `numeric: true`
  - 空态一句话 `<p>还没有扫描记录。</p>`（`scan-history-section.tsx:60-61`）
  - 分页用 `Pagination` + 路由 `Link`，页码越界用 `useClampedPage` 替换历史（`scan-history-section.tsx:46-52`、`68-75`）
  - 每行链接文字相同时，用 `VisuallyHidden` 把时间放进可访问名称（`scan-history-section.tsx:26-31`）——下载链接若每行都叫"下载"可沿用这种写法
- 不分页的短列表：`RootOperationsSection`（`frontend/src/features/library/components/root-operations-section.tsx:16-60`），成功 / 失败用 `StatusTag tone="neutral"/"outline"` 加图标（`root-operations-section.tsx:30-38`），到上限时一句 `只显示最近 100 条记录。`（`root-operations-section.tsx:53`）

### 共用组件（`frontend/src/shared/ui/index.ts:2-19`）

| 组件 | 位置 | 要点 |
|---|---|---|
| `Page` | `shared/ui/page.tsx:18-32` | 唯一 h1，同时设置文档标题；可选 `description`、`actions` |
| `Panel` | `shared/ui/panel.tsx:18-31` | `section` + h2，`action` 放标题右侧 |
| `QueryContent` | `shared/ui/query-content.tsx:24-38` | 加载 / 失败（带请求 ID 和"重试"）/ 成功 / 刷新失败仍显示旧数据 |
| `FailureNotice` | `shared/ui/failure-notice.tsx:18-29` | 按错误码选文案（`messages`），`context="write"` 用于写操作 |
| `Notice` | `shared/ui/notice.tsx:28-45` | `tone` 为 info/success/warning/error；错误 `role="alert"`，其余 `role="status"`；不用全局 toast |
| `Button` | `shared/ui/button.tsx` | 忙碌态不设 `disabled`，用 `busy` + 调用方改文案"正在…"（`.trellis/spec/frontend/component-guidelines.md:34`） |
| `DataTable` / `Column` | `shared/ui/data-table.tsx:5-25` | 真 `<table>`，必须给 `caption`；容器窄于 640px 时堆叠；空列表由调用方显示空态，不渲染空表（`data-table.tsx:30`） |
| `StatusTag`、`Skeleton`、`Pagination`、`VisuallyHidden`、`ConfirmDialog`、`StatList` | `shared/ui/` | 见 `component-guidelines.md:27-38` |

### API 客户端写法

- 唯一的请求函数 `apiRequest`（`frontend/src/shared/api/request.ts:54-104`）：同源、带 Cookie、`cache: "no-store"`、带 `X-Request-ID`，**只接受 JSON 响应**（`request.ts:33-46`、`78`：非 JSON 抛 `invalid_response`）。路径类型是 `` `/api/v1/${string}` ``（`request.ts:5`）。
- 返回 `{ status, data }`，状态码有业务含义（`request.ts:17-18`），例如扫描开始 202/200。
- endpoint 写法：`frontend/src/features/library/api/endpoints.ts:58-65`（`fetchActiveScan`、`startScan`），分页查询拼 `URLSearchParams`（`endpoints.ts:68-71`）。
- 解码器：`import { decode as d } from "shared/api"`，`d.object / d.uuid / d.timestamp / d.integer / d.oneOf / d.optional / d.array(…, { max })`，`d.defineDecoder` 导出，`d.Infer` 取类型（`frontend/src/features/library/api/decoders.ts:1-50`）。数组必须写上限（`d.unbounded` 需注释说明理由，`decoders.ts:24-25`）。
- 查询键带会话代次：`viewerQueryKey(generation, "library", …)`（`frontend/src/features/library/query-keys.ts:1-18`）。
- 错误码文案映射：`frontend/src/features/library/components/root-messages.ts:1-14`（`as const` 对象，按错误码选文案，不比较后端 message；规范 `component-guidelines.md:47-48`）。

### 轮询 / 刷新方式

- 查询 hook：`useActiveScan`（`frontend/src/features/library/hooks/use-active-scan.ts:16-38`）——`refetchInterval` 用函数形式，只在有活动扫描时返回 5 秒，否则 `false`（`use-active-scan.ts:23`）；活动扫描 ID 变化时让扫描记录失效（`use-active-scan.ts:28-34`）。
- `useScan`（`frontend/src/features/library/hooks/use-scan.ts:17-41`）：运行中每 2 秒、`staleTime: 0`，到终态停止并让相关查询失效。
- 命令 hook：`useStartScan`（`frontend/src/features/library/hooks/use-scan-commands.ts:11-21`）——`useMutation`，`onSettled` 里 `invalidateQueries`，返回 Promise 让按钮忙碌到刷新完成。
- 列表不轮询，由事件失效（`use-scan-history.ts:9-21`，`keepPreviousData` 防翻页闪空）。
- 规范：`.trellis/spec/frontend/state-management.md:53-57`（默认 `staleTime` 30 秒、只对网络失败与 503 重试、轮询只在有意义时、页面不可见时暂停）；`.trellis/spec/frontend/hook-guidelines.md:18`（"轮询用 `refetchInterval` 的函数形式，只在非终态返回间隔"）、`hook-guidelines.md:21-22`（`onSettled` 返回刷新 Promise；回调先 `scope.isCurrent()`）。

### 错误提示方式

- 读取失败：`QueryContent` 自动给 `FailureNotice` + "重试"（`query-content.tsx:28-36`）。
- 写失败：区段内就近 `FailureNotice context="write" messages={…} action={重试}`；结果就近 `Notice`，不用 toast（`component-guidelines.md:33`、`frontend/DESIGN-components.md:18`）。

## 3. 文件下载的前端先例

- 全仓 `frontend/src`、`frontend/e2e` 中搜索 `download`、`createObjectURL`、`.blob()`、`Content-Disposition`：**没有任何结果**，前端还没有文件下载功能。
- 最接近的先例是"浏览器直接请求的非 JSON 资源，不经过 `apiRequest`"：
  - 封面：`artworkPath()` 返回 `/api/v1/artworks/<id>` 给 `<img src>`（`frontend/src/features/catalog/api/endpoints.ts:83-89`，注释写明"不经过 apiRequest：它不是 JSON"；使用处 `frontend/src/features/catalog/components/cover.tsx:44`）。
  - 音频：预检返回的 `/api/v1/tracks/<UUID>/audio` 交给媒体元素直接请求，解码器严格校验 URL 形状（`frontend/src/features/player/api/decoders.ts:23`、`55`）。
- 两者都是同源 + 会话 Cookie 的直链；`apiRequest` 只接受 JSON（`request.ts:78`），所以下载二进制文件不能直接走它。
- 开发服务器把 `/api` 代理到 8080（`frontend/vite.config.dev.ts:12`），直链在开发和生产都是同源。

## 4. 应用版本号显示

- 前端**没有任何显示应用版本号的地方**：搜索 `import.meta.env`、`__APP_VERSION__`、`appVersion`、`version` 都没有相关结果；`vite.config.ts` 没有 `define`（`frontend/vite.config.ts:9-39`）。
- `frontend/package.json` 的 `"version": "0.1.0"` 没有被代码读取。
- 品牌区只有文字 "ROOMusic"（`frontend/src/app/shell/brand.tsx:4-9`）；侧栏底部是身份区 `IdentityPanel`（用户名、角色标签、修改密码、退出，`frontend/src/app/shell/identity-panel.tsx:7-25`），两处都没有版本。
- 后端侧：`deploy/production/Dockerfile:9` 的 `go build -ldflags="-s -w"` 没有注入版本变量；`backend/internal/app/app.go:93-94` 只有 `/healthz`、`/readyz`；没有找到返回版本号的 API。
- 现有"所有登录用户都读"的状态接口有 `/api/v1/catalog/status`（`frontend/src/features/catalog/api/endpoints.ts:75`）和 `/api/v1/setup/status`（`frontend/src/features/auth/api/endpoints.ts:18`，`session: "none"`），都不含版本字段。

## 5. 文案规范与测试位置

### 文案 / 空状态约定

- 用户全局约定（`~/.claude/CLAUDE.md` 的"前端界面文案"）：不写防御性说明（不解释权限、隔离、安全、实现细节）；标题、按钮和数据能说清楚的不再加说明；必要的说明用一句日常的话。
- `frontend/DESIGN-acceptance.md:49`：文案使用简体中文；未知来源明确写未知；敏感主机路径、凭据不进入截图、日志或演示数据。
- `frontend/DESIGN.md:143`：关键错误、确认、不可逆说明不用"辅助小字"降级。
- `frontend/DESIGN-components.md:64`：网络或服务异常时说明可以重试的动作，不泄露堆栈或主机路径。
- `.trellis/spec/frontend/quality-guidelines.md`（Review Checklist）："No token, secret, or server path reaches browser storage or display"；目录接口只返回目录名不返回绝对路径（`frontend/src/features/library/api/decoders.ts:13`）。
- 空状态现有写法都是一句短句：`没有正在运行的扫描。`（`scan-section.tsx:35`）、`还没有扫描记录。`（`scan-history-section.tsx:61`）、`还没有目录操作记录。`（`root-operations-section.tsx:49`）；缺失值用 `UNKNOWN_TIME = "—"`（`frontend/src/shared/lib/time.ts:2`）。
- 规范要求每个数据区块都处理加载 / 空 / 错误 / 就绪（`component-guidelines.md:134`）；加载用保留结构的 `Skeleton`，"不画成空库"（`component-guidelines.md:32`）。
- 停用 / 撤销类操作先确认并说明影响，恢复类直接执行（`component-guidelines.md:46`）。

### 可复用的格式化工具

- 时间：`formatDateTime(value)` → `YYYY-MM-DD HH:mm`，浏览器时区（`frontend/src/shared/lib/time.ts:8-22`）。
- 计数：`formatCount`（`frontend/src/shared/lib/number.ts:1-6`）。
- **没有文件大小格式化函数**：搜索 `formatBytes`、`MB`、`GiB` 等无结果；唯一的 `size_bytes` 字段在播放预检解码器里（`frontend/src/features/player/api/decoders.ts:32`），界面上没有显示。

### 测试位置与分层

| 层 | 位置 | 说明 |
|---|---|---|
| 单元 / 解码器 | 与被测文件同目录 `*.test.ts(x)`，如 `frontend/src/features/library/api/decoders.test.ts` | Vitest，jsdom，`frontend/vite.config.ts:33-38` |
| 样本契约 | `frontend/src/test/api-contract.test.ts` | 后端集成测试写出的样本 `backend/internal/app/testdata/api-samples/<资源>/*.json`（读取器 `frontend/src/test/api-samples.ts:4-6`），新接口的样本都要有 decoder 并往返无损 |
| 页面级 | `frontend/src/app/*.test.tsx`，如 `frontend/src/app/library-scans.test.tsx`、`library-roots.test.tsx`、`members.test.tsx` | 经 `renderApp` + 替身后端 `installFakeApi`/`installFakeSession`/`installLibraryApi`（`library-scans.test.tsx:12-19`）；轮询用假定时器推进 |
| 替身后端 | `frontend/src/test/fake-*.ts`（如 `fake-library.ts:36-50`） | 种子数据取自后端样本 |
| 浏览器冒烟（mock） | `frontend/e2e/*.spec.ts`，受控后端 `frontend/e2e/support/api-mock.ts` | `frontend/playwright.config.ts`：webServer 先 `npm run build` 再 `vite preview`（`playwright.config.ts:23-28`），不需要后端；e2e 只能导入纯模块与 decoder，不能导入带 `*.module.css` 的应用代码（`api-mock.ts:40-42`） |
| 真实服务 | `frontend/e2e/real/*.real.ts`，配置 `frontend/playwright.real.config.ts` | 只能经 `scripts/playback-e2e.sh` 启动（`playwright.real.config.ts:4-6`），真实 Go + `roomusic-test` PostgreSQL；与 `test-integration.sh` 不能并行（`.trellis/spec/frontend/quality-verification-layers.md:15-22`） |

- 各阶段的最小 Vitest 命令写法见 `.trellis/spec/frontend/quality-verification-layers.md`（如 `:32`、`:51`、`:69`）。
- 本机跑 Playwright 前需 `source /home/qlqq/.local/share/playwright-deps/env.sh`（`/home/qlqq/workspace/AGENTS.md`）。
- mock 冒烟的 webServer 会重写 `backend/cmd/roomusic/web`，跑完要确认 `git status` 干净。

## 6. 构建产物嵌入后端

- 输出目录：`frontend/vite.config.ts:12-15`，`outDir: "../backend/cmd/roomusic/web"`，`emptyOutDir: true`。
- 嵌入：`backend/cmd/roomusic/main.go:26-30`，`//go:embed web` → `frontendAssets`。
- `frontend/public/licenses/` 构建时复制到产物根目录（`directory-structure.md:16`）。
- 产物**随源码提交**（`git ls-files backend/cmd/roomusic/web` 有文件）；CI 在构建后跑 `git diff --exit-code -- backend/cmd/roomusic/web` 并检查未跟踪文件（`.trellis/spec/backend/core0-runtime-contracts.md:162`、`.trellis/spec/frontend/quality-guidelines.md:116-118`）。改前端源码的提交必须重建并一起提交（`README.md:187`）。
- 重建命令：
  - `cd frontend && npm run build`（= `tsc --noEmit && vite build`，`frontend/package.json` scripts）
  - 或 `make frontend-build`（`Makefile:30`， `frontend-build: cd frontend && npm run build`）
  - 检查：`make frontend-check`（`Makefile:27`，lint + typecheck + vitest）
- 生产镜像不装 Node，直接用已提交的产物：`deploy/production/Dockerfile:2`、`:8`（`test -f cmd/roomusic/web/index.html`）。
- 主检出缺新依赖时 typecheck/build 会报 TS2307，需要在 `frontend/` 下 `npm ci`（auto-memory 的环境说明）。

## 没找到 / 不确定的

- 没有文件下载先例（`a[download]` 或 fetch blob 都没有）。
- 没有版本号的显示和接口；后端构建也没有注入版本。
- 没有文件大小格式化工具。
- 没有"系统 / 设置"类的导航分组或页面。
- 后端备份 API 尚不存在，接口形状（状态码、字段、下载路径）本调研没有涉及，以后端 / 接口合同为准。
