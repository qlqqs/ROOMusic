# M1a 技术设计

需求与验收见 [prd.md](prd.md)。本文记录实现边界、数据流、合同与取舍；版本号是 2026-10-01 的核对结果，实施第一步已复核。
2026-10-01 实施完成后，第 11 至 13 节据实际交付同步，与规划初稿的差异集中在第 12 节。

## 1. 边界

- 只消费现有 `/api/v1` 接口，不改后端生产代码。后端只新增一个生成 API 样本的集成测试与其 testdata，并让集成测试的
  应用装配提供可取消的扫描上下文、在清理时取消并等待扫描 worker 退出（测试辅助代码）。
- 前端从零重建，不搬运旧 `api.ts` / `main.tsx`；旧代码只作合同参考（字段、上限）。
- Router 只拥有 URL、导航与守卫；Query 是服务器状态唯一的缓存。路由不使用 loader，页面直接调用 feature 查询 hook，
  避免 Router 缓存与 Query 缓存并存。
- 组件不直接 `fetch`、不解析原始 JSON；角色只决定显示，所有授权以后端响应为准。

## 2. 依赖清单

现有依赖按 lock 已解析的版本精确固定；新增依赖取核对时的最新稳定版。

| 包 | 版本 | 类别 | 用途 | 许可 |
| --- | --- | --- | --- | --- |
| react、react-dom | 19.2.8 | 运行 | UI | MIT |
| @tanstack/react-router | 1.170.40 | 运行 | 路由与守卫（代码定义，不用 router-plugin） | MIT |
| @tanstack/react-query | 5.104.0 | 运行 | 服务器状态 | MIT |
| lucide-react | 1.41.0 | 运行 | 线性图标 | ISC |
| @fontsource-variable/cormorant-garamond、lora、noto-serif-sc | 5.3.0 | 运行 | 自托管字体 | OFL-1.1 |
| typescript | 6.0.3 | 开发 | 类型检查 | Apache-2.0 |
| vite、@vitejs/plugin-react | 8.2.2、6.1.1 | 开发 | 构建 | MIT |
| vitest、jsdom | 4.1.11、30.0.1 | 开发 | 单元与组件测试 | MIT |
| @testing-library/react、dom、user-event | 16.3.3、10.4.2、14.6.7 | 开发 | 组件交互测试 | MIT |
| @playwright/test | 1.63.0 | 开发 | 浏览器冒烟 | Apache-2.0 |
| @axe-core/playwright | 4.13.0 | 开发 | 无障碍扫描 | MPL-2.0（仅测试使用，不进入产物） |
| @types/node | 24.19.0 | 开发 | 配置、样本读取与 e2e 的 Node 类型 | MIT |
| @types/react、@types/react-dom | 19.2.18、19.2.5 | 开发 | 类型 | MIT |
| eslint、@eslint/js、@typescript-eslint/*、eslint-plugin-react-hooks | 10.9.1、10.0.1、8.69.0、7.1.1 | 开发 | 静态检查 | MIT |

已核对的兼容关系：Vitest 4.1 支持 Vite 6–8；plugin-react 6.1 要求 Vite 8；Testing Library 16 支持 React 18/19；
Router 与 Query 的 peer 要求 React 18/19，Router 要求 Node ≥20.19。TypeScript 6 与 Router 类型的兼容性在第一步验证。

## 3. 目录结构

文件名统一小写短横线；组件以 PascalCase 导出；样式模块与组件同目录，命名 `*.module.css`；测试与被测单元同目录。

```text
frontend/
├── e2e/                          # Playwright 冒烟；受控响应取自 API 样本
│   ├── support/api-mock.ts
│   └── *.spec.ts
├── playwright.config.ts
├── public/licenses/              # 三套字体的 OFL 文本，构建时复制到产物根目录
└── src/
    ├── main.tsx
    ├── app/
    │   ├── providers.tsx         # QueryClient、会话控制器、Router 的装配与清理接线
    │   ├── router.tsx            # 路由树与守卫
    │   ├── shell/                # AppShell、侧栏、窄屏导航、身份区
    │   └── routes/               # 页面组合：概览、扫描目录、扫描详情、成员、登录、初始化、无权限、不存在、服务不可用
    ├── features/
    │   ├── auth/                 # 初始化、登录、退出、当前会话、会话代次与跨标签页同步
    │   ├── members/              # 成员列表与管理
    │   ├── library/              # 目录、目录操作记录、扫描与诊断
    │   └── catalog/              # ReleaseGroup 列表合同；M1a 只用于概览计数，M1b 扩展
    ├── shared/
    │   ├── api/                  # 请求、错误分类、解码原语、错误信封 decoder
    │   ├── ui/                   # 无策略的表现组件
    │   └── lib/                  # 幂等键、请求 ID、时间格式化
    ├── styles/                   # tokens.css、base.css、fonts.ts
    └── test/                     # setup.ts、api-samples.ts、契约测试
```

成员管理单独成 `members` feature：`auth` 只管当前查看者的会话生命周期，管理其他账号是管理员功能，二者的状态和失效时机不同。
`auth` 通过 `index.ts` 公开会话 hook、查看者类型与代次守卫；其他 feature 只经这个入口使用。

## 4. 请求层与解码（`shared/api`）

```ts
type ApiFailure =
  | { kind: "http"; status: number; code: string; message: string; requestId: string }
  | { kind: "invalid_response"; status: number; requestId: string }       // 收到响应但不可信
  | { kind: "network"; requestId: string }   // 写请求结果未知
  | { kind: "aborted" };

function apiRequest<T>(
  spec: { method: "GET" | "POST" | "PATCH"; path: string; body?: unknown; idempotencyKey?: string;
          signal?: AbortSignal; session?: "required" | "none" },
  decode: (input: unknown) => T,
): Promise<{ status: number; data: T }>;   // 失败时抛出携带 ApiFailure 的 ApiError
```

- 返回状态码，因为 202/200（扫描开始、取消）与 201（目录新增）有业务含义。
- `credentials: "same-origin"`、`cache: "no-store"`；有请求体时 `Content-Type: application/json`。浏览器对同源写请求自带
  `Origin`，后端照常校验。
- `X-Request-ID` 为 `web-` 加 16 位十六进制；幂等键为 UUID v4。两者都用 `crypto.getRandomValues` 生成，因为
  `crypto.randomUUID` 在局域网 HTTP 开发访问（非安全上下文）下不可用。
- 响应 `Content-Type` 不是 JSON（例如 HTML 回落页）、JSON 解析失败、信封不合法或 decoder 抛错，都归为 `invalid_response`，
  结果不进入缓存。信封要求 `error.code` 匹配 `^[a-z0-9_]{1,64}$`、`message` 为字符串（≤1000 字符）、`request_id` 为字符串。
- `session: "required"`（默认）的请求得到 401 `unauthorized` 时通知应用注册的会话边界；初始化状态、初始化与登录接口标为
  `"none"`。会话边界是 `{ currentGeneration(), onUnauthorized(generation) }`：请求发出时读取会话代次，401 到达时带回，
  会话控制器据此忽略属于旧会话的 401（旧会话的请求不能结束新登录的会话）。
- 解码原语（`shared/api/decode.ts`）：对象、字符串（长度上下界，默认最长 4096 字符）、布尔、安全整数（上下界）、
  RFC 3339 时间（允许小数秒与时区偏移）、UUID、枚举、数组（先查长度再逐项映射）、可空。抛出带字段路径的 `DecodeError`，
  只用于测试诊断，不展示给用户。
- 列表上限沿用后端：目录操作记录、诊断明细与诊断类别各 ≤100，ReleaseGroup 列表 ≤ `page_size`（≤100）。目录与成员列表
  后端没有上限，decoder 不限条数，只限字段长度（记为后端缺口）。

## 5. 会话生命周期（`features/auth/session`）

```ts
type SessionState =
  | { status: "checking" }
  | { status: "unavailable"; failure: ApiFailure }          // 启动失败，可重试
  | { status: "setup_required" }
  | { status: "anonymous"; notice: "signed_out" | "expired" | null }
  | { status: "authenticated"; viewer: { username: string; role: "admin" | "user" }; generation: number };
```

- 会话控制器是普通 TypeScript 对象，组件经 `useSyncExternalStore` 读取。所有转换串行执行（前一个结束才处理下一个），
  登录与退出不会交错。
- 代次单调递增：进入 `authenticated` 时递增，离开时也递增。查看者标识用用户名（`/auth/me` 没有用户 ID，用户名唯一）。
- 离开 `authenticated` 或查看者变化时，控制器调用应用注册的清理回调，顺序为：`queryClient.cancelQueries()` →
  `queryClient.clear()`（查询与写操作缓存）→ `router.invalidate()` 重新执行守卫 → 导航。
- 三重防护避免旧身份的数据回流：查询键以 `["viewer", generation]` 开头；已登录布局以代次为 `key` 重新挂载；
  写操作 hook 在 `mutate` 时记录代次，成功或失败回调先检查代次再更新界面或导航。
- 跨标签页：`BroadcastChannel("roomusic:session")` 在登录、退出、会话失效后发送 `{ type: "changed" }`；收到后与页面重新
  可见（距上次核对 ≥30 秒）时调用 `/auth/me` 核对。同一用户不做处理；用户不同或 401 时按上文清理。浏览器不支持
  BroadcastChannel 时只依赖重新可见时的核对。
- 初始化成功后用同一凭据自动登录；自动登录失败则回到登录页并预填用户名，提示“管理员已创建，请登录”。
- 退出：`POST /auth/logout` 成功后才清理；失败时保留当前界面并提示重试。

## 6. 路由

```text
root（context: 会话快照）                     notFound → “页面不存在”
├── /setup        公开；不需要初始化时重定向到 /login 或 /
├── /login        公开；validateSearch → { redirect?: 站内路径 }；已登录时重定向
└── 已登录布局（无路径）  需要初始化 → /setup；未登录 → /login?redirect=<当前地址>
    ├── /                         概览
    ├── /library                  扫描目录（管理员）
    ├── /library/scans/$scanId    扫描详情（管理员；参数不是 UUID 时显示扫描不存在，不发请求）
    └── /members                  成员（管理员）
```

- `redirect` 只接受以单个 `/` 开头、长度 ≤2048 的路径，拒绝 `//`、`/\`、协议与 `/login`、`/setup`；不合法时丢弃。
- 管理页在组件中判断角色，非管理员显示“需要管理员权限”，不调用管理接口；后端仍然独立鉴权。
- 会话变化时调用 `router.invalidate()`；页面标题由各页面设置为“页面名 · ROOMusic”。

## 7. 页面与组件

| 页面 | 数据 | 主要状态与交互 |
| --- | --- | --- |
| 初始化 / 登录 | `setup/status`、`setup/admin`、`auth/login` | 居中纸面表单；字段级错误；提交中禁用；会话过期与已退出各有提示 |
| 外壳 | 会话快照 | 侧栏 / 窄屏导航（原生 `<dialog>` 模态，打开时焦点进入、Esc 关闭、导航后关闭并恢复焦点）；身份区与退出 |
| 概览 | `release-groups`（两次，仅取总数）、`scans/active`；管理员另取 `library-roots`、`users` | 区块独立的加载、错误重试与空态 |
| 扫描目录 | `scans/active`、`library-roots`、`library-root-operations` | 依次为扫描、目录、目录操作记录三个区段；新增表单、停用确认、恢复、409 与结果未知的处理 |
| 扫描详情 | `scans/{id}`、`scans/{id}/diagnostics`、`scans/{id}/cancel` | 轮询与停止、取消请求中、六种状态说明、诊断汇总与明细 |
| 成员 | `users` 及其写接口 | 新建表单、停用确认、重新启用、撤销会话确认、针对自己的提示 |

- `shared/ui` 只放至少两处使用、与策略无关的表现组件：Button（主要 / 次要 / 文字）、TextField（label、说明、错误关联）、
  ConfirmDialog（原生 `<dialog>`，有名称、焦点进入与恢复）、Notice（行内状态或错误，可附请求 ID）、StatusTag（文字加
  图标，不只靠颜色）、Skeleton。不建立全局 toast：操作结果就近显示在 `role="status"` 区域。
- 扫描状态只在状态变化时经礼貌型 live region 播报一次，不播报轮询。
- 诊断类别的中文名在 `library` 中维护；类别清单以实施时后端写入 `scan_diagnostics` 的位置为准逐一核对，未知类别原样显示。
- 用户名、目录名等用户内容允许任意换行（`overflow-wrap: anywhere`），超长值不撑破布局。

## 8. 样式、字体与响应式

- `tokens.css` 原样复制 `_ds` 的色板、色阶、间距、圆角与阴影变量，另定义：语义文字色（主文字、次要 `neutral-700`、强调
  `accent-700`）、焦点环（2px `accent-700`，外偏移 2px）、布局（侧栏 216px、页面内边距、内容上限 1240px）和字号刻度
  （12 / 14 / 15 / 17 / 20 / 25 / 36 / 48px；标题 600，48px 展示标题 400）。
- 主按钮沿用透明底与 `accent` 描边，文字改用 `accent-700`：原型的 `accent` 文字对纸面只有约 3.02:1。
- 字体栈：标题 `"Cormorant Garamond Variable", "Noto Serif SC Variable", <中文衬线回退>, serif`，正文把首项换成
  `"Lora Variable"`。中文衬线回退为 `"Noto Serif CJK SC", "Source Han Serif SC", "Songti SC", "STSong", serif`。
  只引入各包的 `wght.css`（正体），不引入斜体；`font-display: swap` 沿用包内设置。
- 许可：把三个包的 `LICENSE` 复制到 `public/licenses/`，并写明来源包与版本；升级字体包时一并更新。按原样再分发包内文件，
  不自行修改或重命名字体（Lora 声明了保留字体名）。
- 断点：≥1200px 桌面留白；768–1199px 缩小页面留白，侧栏保留；<768px 顶部栏加模态导航，表格改为逐行堆叠（每个值带标签），
  不产生整页横向滚动。支持 `prefers-reduced-motion`，界面只有必要的颜色与背景过渡，不用 `transition: all`。

## 9. 接口样本

### 生成与比较

- 测试 `TestPostgreSQLAPIResponseSamples` 位于 `backend/internal/app/api_samples_integration_test.go`，随
  `./scripts/test-integration.sh` 运行。按固定流程调用真实 handler：固定用户名（管理员 `curator`、成员 `listener`、
  `archivist`）、固定目录名（允许根下的 `classical-a`、`classical-b`）、固定请求头 `X-Request-ID: sample-request`。
  初始化与登录都走真实接口（初始化 → 登录取 Cookie），不用直接插入用户。
- 扫描素材是临时目录里的合成文件：`classical-a` 下一个有效 FLAC，`classical-b` 下一个损坏文件。第一次扫描覆盖两个目录，
  真实结果是 `incomplete` 并产生 `parse_failure` 诊断；停用 `classical-b` 后的第二次扫描是 `succeeded`，并产生发行组。
- 需要固定状态的数据直接写库：失败的扫描（带多类诊断）、取消请求中与已取消的扫描、一条失败的目录操作记录、一条封面记录。
  它们是后端表允许、真实流程不一定产出的状态，由真实 handler 读出。
- 时序控制：第一次扫描开始前，测试事务先独占 `library_roots`，worker 读不到目录而停在第一步，因此“新建（202）”“进行中”
  “已有扫描（200，同进程内已有运行中的扫描时后端直接返回它）”都发生在 `running` 状态；释放后轮询到终态并等待 worker 退出，
  再启动下一次扫描。
- 规范化：用 `UseNumber` 解码；每个文件内，UUID 字符串按首次出现顺序替换为 `00000000-0000-4000-8000-000000000001` 起的
  固定值；所有 RFC 3339 时间按先后顺序映射为 `2026-01-01T00:00:00Z` 起逐秒递增的值（相同时刻得到相同值，保留先后关系）；
  按键排序、两空格缩进、末尾换行写出。每个文件为 `{ "status": <状态码>, "body": <响应体> }`。
- 默认逐字比较，目录中出现本次没有产出的文件也失败；失败信息说明如何在确认合同变更后用
  `ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh` 重写并同步前端 decoder。更新模式会删除多余文件。
- 测试断言样本中没有允许根目录的绝对路径、临时目录前缀、`password`、`roomusic_session`、各会话令牌与测试密码。

### 覆盖清单

| 范围 | 样本（目录/文件） |
| --- | --- |
| 初始化与会话 | `auth/`：初始化状态（需要 / 已完成）、初始化 201、登录、当前用户（管理员 / 成员）、退出 |
| 成员 | `users/`：列表（含停用成员）、新建、停用、重新启用、撤销会话 |
| 目录 | `library-roots/`：列表（空 / 启用与停用各一）、新增、重复登记已停用目录、停用、恢复 |
| 目录操作记录 | `library-root-operations/`：列表（空 / 成功与失败的记录，字段可缺失） |
| 扫描 | `scans/`：开始（202 新建、200 已有）、活动扫描（无 / 运行中）、指定扫描（运行中、取消请求中、成功、失败、已取消、不完整）、取消（202、200）、诊断（真实一条、空、多类别） |
| 发行组（概览用） | `release-groups/`：列表（空、一项、一项带封面） |
| 错误信封 | `errors/`：`unauthorized`、`forbidden`、`origin_forbidden`、`invalid_credentials`、`setup_closed`、`invalid_input`（短密码、缺少幂等键）、`username_taken`、`last_admin`、`invalid_library_path`、`revision_conflict`、`idempotency_conflict`、`not_found`（扫描、未知 API）、`invalid_id`、`method_not_allowed`、`database_unavailable`（降级 handler） |

### 消费

- `src/test/api-samples.ts` 从 `../backend/internal/app/testdata/api-samples/` 读取样本（Node `fs`），是唯一记录该路径的地方。
- `src/test/api-contract.test.ts` 遍历全部样本：每个样本必须在映射表中对应一个 decoder（错误样本对应错误信封 decoder）
  并解码成功、状态码一致，且 decoder 往返后与样本相同（后端新增字段而 decoder 没有保留它时失败）；没有映射的样本，
  或没有样本的映射，同样失败。
- `e2e/support/api-mock.ts` 用同一批样本响应 `page.route`；场景需要的变体（例如列表多一项）在样本数据上修改字段值，
  不手写新的响应结构。

## 10. 测试

- **单元与组件（Vitest + jsdom + Testing Library）**：请求层（内容类型、损坏 JSON、HTML、信封、网络失败、取消、401 回调
  只对受保护请求触发、请求头）；解码原语与每个 decoder（样本与反例）；会话控制器（启动分支、代次、清理顺序、迟到响应
  被忽略、广播与重新可见核对、转换串行）；`redirect` 校验、幂等键、UTF-8 密码长度；各页面在桩 `fetch`（返回样本）下的
  成功、错误、409、结果未知与重试、扫描轮询在终态停止（假定时器）、非管理员不请求管理接口。
- **浏览器冒烟（Playwright，Chromium）**：对生产构建运行 `vite preview`，API 全部受控。覆盖初始化后自动登录、登录跳回、退出、
  会话中途 401、403、目录操作、扫描开始到终态、成员操作、只用键盘完成登录与确认对话框、无第三方请求；五种宽度下各页
  `scrollWidth <= innerWidth`；axe 以 `wcag2a`、`wcag2aa`、`wcag21a`、`wcag21aa` 标签扫描，serious 或 critical 即失败。
- **真实接口冒烟（一次，记录在任务 `research/`）**：`compose.test.yaml` 以独立 project 名启动 PostgreSQL；Go 以内嵌产物启动，
  允许根为临时目录，放入一个合成的有效 FLAC 与一个损坏文件；用 Playwright 脚本走 A2 的主路径，结束后清理容器与目录。

## 11. CI 与仓库

- 前端作业：npm ci → lint → typecheck → `npm run test` → `npm run build` → 内嵌资产漂移检查（沿用现有两条命令）→
  `npx playwright install --with-deps chromium` → `npm run test:e2e`。删除入口检测与占位页分支。
- `package.json` 脚本：`test` 改为 `vitest run`（去掉 `--passWithNoTests`），新增 `test:e2e`（`playwright test`）与 `preview`（`vite preview`，
  Playwright 的 webServer 经它启动预览，不依赖 `PATH` 里有 `node_modules/.bin`）。CI 的前端作业超时改为 20 分钟，Playwright 重试一次，
  失败时上传 HTML 报告与 trace。
- `vite.config.ts` 显式设置 `build.target`（`chrome111`、`edge111`、`firefox113`、`safari16.4`），与浏览器矩阵一致；
  ESLint 禁止 `shared/api/request.ts` 之外直接使用 `fetch`（`no-restricted-globals` 与 `no-restricted-properties`）。
- `.gitignore` 增加 `frontend/test-results/`、`frontend/playwright-report/`；Makefile 的 `frontend-install` 改用 `npm ci`，
  新增 `frontend-e2e`。
- 构建产物继续提交：每个改动前端源码的提交都重新构建并一起提交 `backend/cmd/roomusic/web`。

## 12. 与规划初稿的差异

- 字体从“400、600 两个静态字重”改为同一来源的可变字体包：体积约为静态方案的 30%，且 400 与 600 由一个文件提供。
- 浏览器冒烟的宽度从 375 / 900 / 1440 改为 DESIGN.md 要求的 375 / 768 / 1024 / 1280 / 1440。
- 成员管理增加“重新启用”：后端已支持，缺少它时误停用只能改数据库。
- 初稿的待规划事项已决定：会话代次与跨标签页（K1）、依赖与浏览器矩阵和重试（K2）、URL 形状与旧链接（K5）、样本位置与
  CI 归属（K7）。

实施中与设计的差异（取舍与发现见 [implement.md](implement.md) 的“实施中的发现”）：

- 开始扫描返回 200（已有扫描在运行）时不新建也不跳转，留在扫描目录页提示并链接到那个扫描；只有 202 才进入详情页。
- 活动扫描（`/scans/active`）在确有扫描时每 5 秒刷新（页面隐藏时暂停），设计只写了详情页轮询；否则扫描目录页与概览在扫描结束后会一直显示“运行中”。
- 表格以容器宽度（CSS 容器查询，小于 640px）而不是视口宽度决定是否堆叠，因为带侧栏时 768px 视口里内容区只有约 500px；`DataTable` 进入 `shared/ui`。
- `shared/api` 新增 `isOutcomeUncertain`（网络失败、无效响应、5xx 为结果不确定，4xx 为明确拒绝）；成员写操作没有幂等键，所以没有“重试”，只刷新核对。
- `app/runtime.ts`（`createAppRuntime`）是缓存、会话与路由之间唯一的接线点，设计第 3 节的目录图没有列出它。
- 路由：TanStack Router 把 `validateSearch` 的返回值合并到原始查询串，不合法的值必须显式返回 `undefined`（`/login` 的 `redirect` 有回归测试）。
- 数字：Cormorant Garamond 默认是旧式数字，数据值用 `lining-nums tabular-nums`。
- 浏览器测试：受控后端按同一份行为定义另写（不能复用依赖 vitest 的替身）；原生 `<dialog>` 的焦点断言放宽为“在对话框内或在 `body` 上”；增加
  `e2e/checks.spec.ts` 自检，证明各项检查会失败。本机缺系统库且无 root 时的运行方法见前端质量规范。
- 验收的真实接口冒烟用合成 FLAC 与损坏文件，走 A2 主路径共 82 项检查，记录在 `research/real-api-smoke.md`；没有发现产品缺陷。

## 13. 后端缺口（不在本任务修复）

- 没有扫描历史列表：离开扫描详情后，结束的扫描只能通过 URL 找回；扫描目录页只显示活动扫描。
- `/auth/me` 不返回用户 ID：成员页用用户名判断目标是否为自己。
- 成员列表无分页，成员操作无幂等，不能创建管理员或修改角色；`listUsers` 会静默跳过读取失败的行。
- 401 信封文案与角色无关（界面按错误码选文案规避）。
- 没有查询允许根目录的接口，新增目录只能手输绝对路径。
- 封面缓存头为 `private, max-age=86400`，在 M1b 显示封面时评估。
- `createUser` 把任何插入失败都映射成 409 `username_taken`；`invalid_input` 的文案写“12 个字符”而规则按字节；`PATCH /users/{id}` 与撤销会话
  对格式不对的 ID 返回 503（`::uuid` 转换错误）而不是 400。
- `httpserver.Frontend` 不设缓存头，带哈希文件名的资源与较大的字体每次都要重新协商；对 `/assets/`、`/licenses/` 这类目录路径会返回目录列表。

这些缺口已记入 [后端运行合同的技术债](../../spec/backend/core0-runtime-contracts.md)。

## 14. 回退

- 每一步一个提交，前端源码与重新构建的内嵌资产在同一提交；按逆序 `git revert` 即可回到上一可用状态。
- 字体文件在第一步进入历史；推送前可以改写本地提交移除，推送后无法撤出。
- 后端只有测试与 testdata 变化，回退不影响运行行为；数据库无迁移。
