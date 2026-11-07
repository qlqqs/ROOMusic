# Frontend Quality Guidelines

## Status

前端 package 位于 `frontend/`，使用 npm 与 `package-lock.json`，直接依赖全部精确固定（`package.json` 里没有 `latest` 或范围版本）。
2026-10-01 的 M1a 起，CI 与本地都运行真实的前端门禁：没有“源码缺失则跳过”的分支，Vitest 不再使用 `--passWithNoTests`。
Node 版本由 `.mise.toml` 管理，安装使用 `npm ci`。仓库没有 formatter；无障碍由 Playwright 加 axe-core 在浏览器里扫描。

The current [README](../../../README.md) establishes Node.js 24.22.1 through the
repository development toolchain. Do not rely on an undocumented global version.

## Required Practices

- Organize behavior by feature and expose narrow typed contracts.
- Decode all REST data at API boundaries and keep TypeScript strict.
- Separate server, URL, local, form, and session state.
- Keep components accessible and focused on rendering/intent.
- Keep side effects in feature hooks/API clients with cancellation and cleanup.
- Handle loading, empty, stale, error, permission, conflict, canceled, and
  incomplete states that apply to the workflow.
- Keep backend authority visible: the UI handles denied responses and never
  substitutes a role check for security.
- Preserve same-origin opaque-cookie authentication and read-only library
  assumptions.
- Make changes at the owning feature and avoid unrelated visual or dependency
  refactors.

## Forbidden Patterns

- `any`, ignored type errors, raw JSON casts, or unchecked non-null assertions.
- Direct `fetch` calls scattered through components.
- Browser-readable bearer tokens in localStorage, sessionStorage, IndexedDB,
  URLs, or JavaScript state.
- A global store containing copies of server resources.
- Business rules based only on hidden/disabled controls.
- Raw absolute paths, secrets, backend stack traces, or SQL errors in UI.
- Unbounded rendering of the whole music library.
- Snapshot-only tests for meaningful workflows.
- New UI, state, or styling libraries added without a concrete need and recorded
  decision.
- GraphQL, Agent runtime, playback, plugin marketplace, Meilisearch-specific, or
  offline PWA code introduced as Core 0 scaffolding.

## Test Requirements

| Unit | Minimum evidence |
| --- | --- |
| Runtime decoder | valid, missing, malformed, unknown enum, and safe error fallback |
| Pure view projection | Medium/Track ordering, provenance labels, empty values |
| Component | loading/empty/error/ready states, keyboard interaction, accessible names |
| Feature hook | query success/error, cancellation/stale response, mutation conflict, polling stop |
| Auth flow | setup closure as applicable, login, logout, expired/revoked session, 401/403 handling |
| Library admin | allowed input UX, pending state, revision conflict, retry/recovery presentation |
| Catalog/search | Release hierarchy, pagination/filter URL state, no raw path disclosure |
| Cross-layer smoke path | login -> browse -> detail/search; admin root/scan path separately |

Use the selected browser/component runner for behavior. Tests assert outcomes and
accessible interaction rather than implementation calls or incidental markup.
Backend integration tests remain responsible for real authorization, path
containment, transactions, and session cookie flags; frontend tests prove that
the UI sends the contract and handles results correctly.

### 已采用的测试层次（M1a 起，M1b 补充）

| 层次 | 工具与做法 |
| --- | --- |
| 单元与组件 | Vitest + jsdom + Testing Library（`user-event`）。请求层、解码原语、每个 decoder（样本与反例）、会话控制器、`shared/ui` 组件各自有测试，与被测单元同目录 |
| 接口契约 | `src/test/api-contract.test.ts` 遍历后端生成的 JSON 样本，每个样本都要被 decoder 解码且往返无损；样本由后端集成测试 `TestPostgreSQLAPIResponseSamples` 把关 |
| 页面级 | `src/app/*.test.tsx`：经 `renderApp` 装配真实的应用运行时（路由、会话、查询缓存），后端是有状态的替身（幂等重放、`expected_revision` 与 409、可注入请求或响应丢失，响应取自真实样本）；轮询用假定时器；断言用户可见的结果。M1b 的专辑页：`src/test/catalog-data.ts` 按列表里的摘要合成组详情、版本详情与完整证据（任意介质数与曲目数，含千首曲目），可登记特别安排的数据，也能模拟“被重新扫描后不可见的版本” |
| 浏览器冒烟 | Playwright（只跑 Chromium）对**生产构建**运行 `vite preview`，API 由测试内的受控后端响应（`e2e/support/api-mock.ts`，响应体取自样本）。覆盖初始化与登录、回跳与退出、会话中途 401、403、目录与扫描（含扫描记录的翻页与进入详情）、成员、专辑（搜索、筛选、翻页、详情、版本切换、证据权限、封面重新加载）、概览的“最近播放”（卡片、清除确认与失败、读取失败；`api-mock.ts` 的 `seedRecentPlays` 预置记录）、只用键盘完成主要操作（含专辑主路径：搜索 → 筛选 → 翻页 → 打开详情 → 切换版本 → 展开证据 → 返回列表）；每个测试结束时断言没有第三方请求、没有脚本错误与控制台错误 |
| 响应式与无障碍 | 每个页面状态在 375 / 768 / 1024 / 1280 / 1440 下页面与主内容区（`#main`）都没有横向溢出，≥768px 时窗口本身不能再滚动（`expectNoDocumentScroll`）；axe（`wcag2a`、`wcag2aa`、`wcag21a`、`wcag21aa`）在默认、对话框、错误提示、字段错误与窄屏菜单等状态下没有 serious 或 critical；所有文字不小于 12px。M1b 起长内容（超过 1000 字的中日英混排标题、超长艺人名、无空格长串、十二张介质）的列表与详情也在清单里；还有“窗口先宽后窄”的回归用例（滚到底让屏幕外的分段被 `content-visibility` 跳过，再缩到窄屏） |
| 检查自检 | `e2e/checks.spec.ts` 用故意有问题的页面确认 axe、最小字号、横向溢出、窗口纵向滚动与焦点环检查都会失败，避免检查空转时全部通过 |
| 真实接口冒烟 | 在隔离环境（`compose.test.yaml` 以独立 project 名启动 PostgreSQL，Go 以内嵌产物启动，允许根为临时目录，只放合成素材）走主路径，命令与结果记录在任务 `research/`，用后清理容器与目录。不进入 CI，**不读取用户真实的 `music/`**。M1b 的冒烟用 74 个合成 FLAC（含双碟、CUE、需要关注、长标题与竖版封面）走专辑浏览，并发现了后端封面发现的两处缺口（见后端运行合同的技术债）；阶段 P 的真实服务用例（`scripts/playback-e2e.sh`，合成 FLAC 与 180 秒的慢速素材）核对播放过半后的记录、概览显示与清除、只拖动不产生记录 |

浏览器测试的约定：用 `page.clock.install()` 加 `fastForward` 推进轮询，不真实等待、不用固定睡眠；
自检用的页面要写 `<!doctype html>`：没有它浏览器进入怪异模式，`document.documentElement.scrollHeight` 等度量与真实页面不同，检查会“通过”却没有生效；
检查加进去之后用故意去掉修正的变更确认新用例确实会失败（M1b 的 `min-width: 0` 与外壳 `position: relative` 都这样验证过）；夹具回调参数不要命名为 `use`
（react-hooks 的 lint 规则会把它当成 Hook，本项目叫 `provide`）；原生 `<dialog>` 的焦点断言见[组件规范](./component-guidelines.md)。
Playwright 在 CI 里重试一次，失败时上传 HTML 报告与 trace（保留 7 天）；本机复用已在运行的预览服务，CI 不复用。

## Accessibility And Responsive Gate

Run automated accessibility checks on representative screens and manually verify
keyboard focus, dialogs, form errors, landmarks/headings, status announcements,
and contrast. Test narrow mobile and desktop widths. Text, controls, tables, and
cover media must not overlap or resize the surrounding layout unexpectedly.

Automated accessibility output is a signal, not proof. Critical workflows require
keyboard and screen-reader-semantic review. M1a 的自动化部分已在 Playwright 中落地（见上表）；键盘路径用例覆盖
登录、停用确认对话框、成员新建与确认、窄屏菜单与跳转链接，M1b 增加专辑主路径。**人工读屏验证仍未做。**

M1b 从真实页面上学到的布局约束（都由上面的检查守住）：

- 长页面里的“仅供读屏”元素（`VisuallyHidden`、`DataTable` 的 caption）是绝对定位的，滚动容器必须是定位祖先，否则文档被撑高、窗口能滚出一片空白。
- 网格与弹性容器里的项要写 `min-width: 0`；`content-visibility: auto` 的分段被跳过时会保留上一次渲染的宽度，窗口缩窄后仍撑宽轨道。
  占位尺寸只写块方向的 `contain-intrinsic-block-size`，简写 `contain-intrinsic-size` 带一个值会同时给宽度。
- 分页链接在结果下方：点击后把结果标题滚到视野顶部并聚焦，否则新的一页停在滚到底的位置，键盘用户要倒着穿过整页卡片。
- 只改查询参数的导航（切换版本）不应把焦点抢回主内容区；路径变化才回到页面顶部。

## 当前脚本与门禁

`frontend/package.json` 的稳定脚本：

```text
dev          # vite --config vite.config.dev.ts（开发服务器，代理 /api）
lint         # eslint .
typecheck    # tsc --noEmit
test         # vitest run
build        # tsc --noEmit && vite build（产物输出到 backend/cmd/roomusic/web）
preview      # vite preview
test:e2e     # playwright test（webServer 先构建，再用 preview 提供产物）
```

Makefile：`frontend-install`（`npm ci`）、`frontend-check`（lint、typecheck、test）、`frontend-build`、`frontend-e2e`。

CI 前端作业按顺序执行：`npm ci` → lint → typecheck → `npm run test` → `npm run build` → 内嵌资产漂移检查（`git diff --exit-code`
与未跟踪文件检查）→ `npx playwright install --with-deps chromium` → `npm run test:e2e`，失败时上传 Playwright 报告。
构建产物（`backend/cmd/roomusic/web`）随源码提交：**每个改动前端源码的提交都重新构建并一起提交**，否则漂移检查失败。
样本漂移由集成作业（`./scripts/test-integration.sh`）把关。

按改动选择最小相关测试集；仅文档变更检查链接、任务上下文和 diff，不为了“全绿”运行
无关全量测试。实现阶段必须记录实际命令、跳过原因和尚缺证据。生产构建由 Go 服务同源提供，不依赖独立 Node 生产进程。

本机运行浏览器测试：首次需要 `cd frontend && npx playwright install chromium`（CI 用 `--with-deps`）。没有 root 权限且缺少
Chromium 的系统库（`libatk` 等）时，可 `apt-get download` 后 `dpkg -x` 解到临时目录，再用 `LD_LIBRARY_PATH` 指向它运行；
`test-results/` 与 `playwright-report/` 已被 Git 忽略。formatter 若需新增，仍须单独选型。

## 各阶段的验证分层

M2a 播放、M3 受治理操作、M4 默认版本与版本对比、M4 问题处理、M4 认领各自的验证分层（最小 Vitest 命令、共用的接口行为、页面级、浏览器冒烟、真实服务用例）已移到 [quality-verification-layers.md](./quality-verification-layers.md)（本文件超过 24 KB 的上限；内容原样搬移，之后的阶段增补写在那里）。

## Review Checklist

- [ ] Change belongs to one feature or an explicit app/shared owner.
- [ ] Shared extraction has multiple real policy-free consumers.
- [ ] REST input/output is runtime-decoded and typed.
- [ ] State has one source of truth and query identity is complete.
- [ ] Mutation handles pending, success, classified failure, and conflict.
- [ ] Backend authorization remains required and denied responses render safely.
- [ ] No token, secret, or server path reaches browser storage or display.
- [ ] Semantic HTML, keyboard behavior, labels, and focus are verified.
- [ ] Relevant unit/component/cross-layer tests pass.
- [ ] Core 0 production still uses the Go-hosted same-origin build.
- [ ] No future-only infrastructure was made mandatory.

## Anti-Example

A Release page that fetches raw JSON in `useEffect`, casts it, copies it into a
global store, hides an admin button based on a local role string, and renders
every Track at once may compile but violates ownership, type, security, state,
and scale contracts.

See [Engineering Principles](../guides/engineering-principles.md) and the
[Core 0 acceptance criteria](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
