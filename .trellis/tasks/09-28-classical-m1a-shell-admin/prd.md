# M1a：Classical 外壳与管理界面

> 状态：已完成（2026-10-01）。2026-09-28 由[路线修订任务](../archive/2026-09/09-28-core-1-roadmap-revision/design.md)（第二部分）创建，
> 2026-10-01 规划获批并实施：规划提交 `2e84149`，实施提交 `66a0e05`–`4220960`，另有文档同步提交。
> 父任务见 [Classical 重设计 / Core 1](../09-27-classical-core-1/prd.md)。技术设计见 [design.md](design.md)，执行计划与实施发现见
> [implement.md](implement.md)，真实接口冒烟记录见 [research/real-api-smoke.md](research/real-api-smoke.md)。

## 目标与用户价值

前端源码已于 2026-09-27 删除，目前没有可用客户端。本任务按 Classical 设计重建前端，交付可日常使用的外壳、认证、
现有目录/扫描/成员管理和简版概览，恢复 CI 的真实构建门禁，并建立前后端接口防漂移的 JSON 样本机制。
M1b（专辑浏览）在本任务的外壳、数据层与 decoder 约定之上实施。

## 依据

- [ADR 0001](../../../docs/architecture/adr/0001-frontend-foundations.md) 的决策记录：第 1–3 项与接口防漂移做法已批准（D3）；
  第 4、5 项按本任务“关键决策”K1、K2 提请批准。
- [frontend/DESIGN.md](../../../frontend/DESIGN.md) 与 `.trellis/spec/frontend/` 下的各项规范。
- [Core 1 路线](../../../docs/architecture/core-1-roadmap.md) 矩阵中标注 M1a 的行。
- 用户偏好：开发阶段不做兼容，有必要可直接重构。

## 已确认的事实（2026-10-01 核对）

### 工具链、构建与 CI

- `frontend/package.json` 的多数直接依赖写作 `latest`，lock 已解析为 React 19.2.8、TypeScript 6.0.3、Vite 8.2.2、
  @vitejs/plugin-react 6.1.1、Vitest 4.1.11、jsdom 30.0.1、ESLint 10.9.1、typescript-eslint 8.69.0、
  eslint-plugin-react-hooks 7.1.1、lucide-react 1.41.0；构建工具放在 `dependencies`。
- `eslint-plugin-react-hooks` 已安装但 `eslint.config.js` 没有启用；`tsconfig.json` 已开 `strict`；`index.html` 引用缺失的 `/src/main.tsx`。
- Node 24.16.0、npm 11.13.0 由 `.mise.toml` 管理。生产构建输出到 `backend/cmd/roomusic/web`，该目录提交入库并由 Go 内嵌
  （`backend/cmd/roomusic/main.go`）；`httpserver.Frontend` 对非 `assets/` 的未知路径回落到 `index.html`，缺失的 `assets/*` 返回 404。
- CI 前端作业执行 `npm ci`、lint、typecheck 和 `vitest run --passWithNoTests`；只有 `frontend/src/main.tsx` 存在时才构建并检查
  内嵌资产漂移，否则只检查占位页（`.github/workflows/ci.yml`）。集成作业运行 `./scripts/test-integration.sh`，
  只执行名称以 `TestPostgreSQL` 开头的测试。
- 旧前端（`63c3bab` 删除）使用 hash 路由和 `?release=` 选择详情。

### 现有接口中影响界面的行为

接口注册见 `backend/internal/identity/httpapi/handlers.go:30-38`、`backend/internal/library/httpapi/handlers.go:33-42`、
`backend/internal/catalog/httpapi/handlers.go:35-40`。

- `GET /auth/me` 只返回 `{username, role}`，没有用户 ID（`identity/httpapi/handlers.go:133-140`）。
- `POST /setup/admin` 创建管理员但不写会话 Cookie，初始化后仍需登录（`handlers.go:51-93`）。
- 所有写请求要求 `Origin`；会话 Cookie 为 HttpOnly、SameSite=Lax、24 小时（`identity/guard.go:89-126`）。
  401 信封的 message 对所有用户都是“需要管理员登录”（`guard.go:84-86`）。
- 成员：列表无分页；只能创建 `user` 角色；`PATCH {disabled}` 同时支持停用与重新启用，停用会撤销其全部会话；
  撤销会话不检查用户是否存在；停用最后一个管理员返回 409 `last_admin`；成员操作没有幂等键（`handlers.go:142-301`）。
  密码长度按 UTF-8 字节计算（至少 12）；用户名只检查去空白后非空，原样保存。
- 目录：列表只返回目录名（basename）；新增要求 `Idempotency-Key`，路径已登记时返回已有目录（停用的保持停用）且仍为 201；
  停用、恢复要求 `expected_revision` 与幂等键；冲突为 409 `revision_conflict` / `idempotency_conflict`
  （`library/httpapi/roots.go`、`root_operations.go`）。新增时输入的是服务器绝对路径，必须位于
  `ROOMUSIC_ALLOWED_LIBRARY_ROOTS` 之内，没有接口列出允许的根目录（`library/roots.go:12-37`）。
- 目录操作记录：管理员可读最近 100 条（`root_operations.go:114-147`）。
- 停用的目录不参与扫描（`library/scanner.go:206`），也不参与缺失对账；已入库的 Release 保持可见。
- 扫描：开始返回 202（新建）或 200（已有活动扫描）；任何已登录用户可读活动扫描与指定扫描；取消返回 202（已请求）或
  200（已是终态）；诊断仅管理员，最多 100 条；没有扫描历史列表接口（`library/scans.go`、`library/httpapi/scans.go`）。

### 字体实测（Fontsource 5.3.0，均为 OFL-1.1）

| 方案 | Noto Serif SC | Cormorant Garamond + Lora | 说明 |
| --- | --- | --- | --- |
| 静态包 400 + 600 | woff2 9.4 MB + woff 12.2 MB | 约 0.8 MB | CSS 同时引用 woff2 与 woff，Vite 两者都产出，合计约 22 MB |
| 可变字体包（wght 轴） | woff2 6.0 MB，101 个分片 | 0.29 MB | 只有 woff2，单个文件覆盖 400 与 600 |

构建产物提交入库并内嵌进 Go 二进制，字体体积会同时进入 Git 历史与二进制。Lora 声明了保留字体名“Lora”，
其余两套没有；三个包都附带 `LICENSE`。

## 需求

- **R1 脚手架与依赖**：直接依赖全部写成精确版本，构建工具移入 devDependencies，与 lock 一并提交。新增 TanStack Router
  （代码定义路由，不用文件路由插件与生成文件）、TanStack Query、三套 Fontsource 可变字体；开发依赖新增 Testing Library、
  Playwright、axe-core 与 `@types/node`。TypeScript 在 `strict` 上增开 `noUncheckedIndexedAccess`、`exactOptionalPropertyTypes`
  与 `noFallthroughCasesInSwitch`；ESLint 启用 react-hooks 推荐规则。Classical 全局变量集中定义，组件样式用 CSS Modules。
- **R2 共享请求层（`shared/api`）**：同源 `fetch`，携带 Cookie，`cache: "no-store"`，JSON 请求体，传递取消信号，每个请求带
  客户端生成的 `X-Request-ID`。只有 JSON 响应进入解码；失败分为后端错误信封、响应无效（非 JSON、HTML、信封或 decoder
  失败）、网络失败（结果未知）和已取消四类。受保护请求的 401 `unauthorized` 通知会话边界，登录接口的 401
  `invalid_credentials` 不算会话失效。界面按错误码选文案；未知错误码显示后端的安全 message 与请求 ID。
- **R3 认证与会话**：
  - 启动时先查初始化状态，需要初始化则进入 `/setup`，否则用 `/auth/me` 确认会话；启动失败（如 503）显示可重试的整页错误。
  - 初始化：用户名去首尾空白后非空，密码至少 12 个 UTF-8 字节并二次确认；成功后用同一凭据自动登录；
    409 `setup_closed` 转到登录页。
  - 登录：成功后进入 `redirect` 指定的站内路径（只接受以单个 `/` 开头的路径）或概览；登录失败提示用户名或密码错误。
  - 退出：后端确认后才清理并回到登录页；请求失败时提示重试，不假装已退出。
  - 会话代次：每次确认会话与每次结束会话都递增代次。查询键带代次；结束或切换会话时取消并清空查询缓存、重新挂载已登录
    界面（丢弃表单、对话框等私有状态）、重新执行路由守卫；旧代次的请求结果和写操作回调不更新界面、不导航。
  - 跨标签页：用 BroadcastChannel 只广播“会话已变化”（不含凭据与用户数据）；其他标签页收到广播或重新可见时重新核对
    `/auth/me`，用户变化或会话失效即按上条清理。
  - 403 只拒绝当前操作或页面，不结束会话。
- **R4 外壳**：
  - 宽度 ≥768px 时左侧 216px 导航与主内容各自滚动；<768px 时为顶部栏加“菜单”按钮打开的模态导航，页面只有一个滚动区。
  - 导航只列真实可用的页面：所有用户有“概览”，管理员另有“资料库”分组下的“扫描目录”与“成员”；M1b 再加入“专辑”。
    不出现播放条、播放入口、艺人、收藏、Steward 等尚未实现的入口。
  - 身份区显示用户名、角色（管理员 / 成员）和退出登录。
  - 有“跳到主要内容”链接、`nav`/`main` 地标、每页一个 `h1`，页面标题为“页面名 · ROOMusic”。
  - 非管理员直接访问管理页时显示“需要管理员权限”且不请求管理接口；未知路径显示“页面不存在”。
- **R5 目录管理（扫描目录页）**：
  - 列表显示目录名、状态（启用 / 已停用）、更新时间与可用操作。
  - 新增：输入服务器上的绝对路径并说明必须位于允许的音乐根目录内；400 `invalid_library_path` 显示在字段旁；
    返回已登记且停用的目录时说明“已登记，处于停用状态，未自动恢复”。
  - 停用前确认，说明扫描将跳过该目录、已入库内容保留且不会因此标记缺失、之后可恢复；恢复直接执行。两者都携带 `expected_revision`。
  - 每个新的操作意图生成新的幂等键；网络失败等结果未知时，“重试”沿用同一个键；409 `revision_conflict` 刷新列表并说明
    目录已变化；409 `idempotency_conflict` 显示错误并要求重新操作。
  - 目录操作记录：最近 100 条，显示时间、目录名（由目录 ID 对应）、操作、结果、revision 与错误码。
- **R6 扫描**：
  - 扫描目录页显示活动扫描（运行中或取消请求中、开始时间、诊断数）；没有活动扫描时提供“开始扫描”。
  - 开始扫描后进入该扫描的详情页；返回 200 时提示“已有扫描在运行”；503 显示可重试的错误。
  - 详情页在运行中每 2 秒轮询，页面隐藏时暂停，到终态即停止。状态文案区分运行中、取消请求中、成功、失败、已取消、
    不完整，并说明失败、取消、不完整都不会执行缺失对账；不显示百分比或剩余时间。
  - 取消后显示“取消请求已提交，等待扫描停止”，终态以后端为准。
  - 诊断（管理员）：按类别汇总（已知类别显示中文名，未知类别原样显示）并列出最多 100 条明细（类别、安全消息、可选的
    相对路径）；到达终态时刷新一次，运行中可手动刷新。
  - 扫描 ID 不是 UUID 时不发请求，直接显示扫描不存在；404 同样显示扫描不存在。
- **R7 成员**：
  - 列表显示用户名、角色、状态、创建时间与可用操作。
  - 新建：规则同初始化，只能创建普通成员，界面说明当前不能创建管理员或修改角色；409 `username_taken`、
    400 `invalid_input` 显示在字段旁。
  - 停用（确认，说明会立即撤销其全部会话）、重新启用、撤销会话（确认）。目标是自己时额外提示“你会被退出登录”，
    成功后按 401 流程回到登录页；409 `last_admin` 说明不能停用最后一个管理员。
  - 成员操作没有幂等保证：不自动重试，结果未知时刷新列表核对并提示。
- **R8 概览**：
  - 资料库区块：专辑数与需要关注的专辑数，取 `/release-groups` 的 `pagination.total`（分别不带与带 `attention=required`）；
    专辑页尚未交付，数字不做成链接。
  - 扫描区块：活动扫描状态，或“没有正在运行的扫描”；管理员可跳转扫描目录。
  - 管理员另见目录（启用 / 停用数）与成员（启用 / 停用数）区块。
  - 各区块独立加载、失败与重试。空库时对管理员提示添加目录并扫描，对成员说明需等待管理员。不展示容量、新增数、播放历史等
    没有来源的统计。
- **R9 接口防漂移**：
  - Go 集成测试经真实 handler 产生响应，把 UUID、时间戳规范化为固定值、请求 ID 固定后，与提交的 JSON 样本逐字比较；
    不一致或目录里有多余样本即失败。确认是有意的合同变更时用环境变量重写样本。
  - 样本只含状态码与响应体，不含绝对路径、密码或 Cookie，由测试断言。
  - 前端契约测试遍历全部样本，每个样本必须对应一个 decoder 并解码成功；各 decoder 另有缺字段、错类型、未知枚举、超界等
    反例。Playwright 的受控响应同样取自样本。覆盖清单见 design.md。
- **R10 CI**：前端作业删除“前端重置”分支，按 npm ci → lint → typecheck → 单元测试 → 生产构建 → 内嵌资产漂移检查 →
  安装 Chromium → Playwright 冒烟执行。集成作业不变，样本漂移在其中失败。Playwright 的输出目录被 Git 忽略。
- **R11 文档**：同步前端各规范（目录、状态、类型、质量、hook、组件）、后端运行合同中的 CI 与样本说明、ADR 0001 决策记录、
  DESIGN.md（字体、字号、断点）、README、Core 1 路线与架构图、父任务和 M1b 的 PRD。

## 验收标准

| 编号 | 需求 | 可观察结果 |
| --- | --- | --- |
| A1 | R1、R10 | `package.json` 没有 `latest` 或范围版本；`npm ci`、`npm run lint`、`npm run typecheck`、`npm run test`、`npm run build` 通过；构建结果与提交的 `backend/cmd/roomusic/web` 一致；Go 提供的页面在 `/library`、`/members` 等深链刷新后正常加载，缺失的 `/assets/*` 返回 404，未知 `/api/*` 仍返回 JSON 404 |
| A2 | R3–R8 | 在隔离的 PostgreSQL 与临时目录（合成文件，不用真实音乐）上走通：初始化 → 自动登录 → 添加目录 → 开始扫描并看到终态与诊断 → 停用并恢复目录 → 新建成员 → 停用并重新启用 → 撤销会话 → 退出；记录命令与结果 |
| A3 | R2、R3、R5、R7 | 401 结束会话并回到登录页；403 显示拒绝且会话保留；`revision_conflict`、`idempotency_conflict`、`last_admin`、`username_taken`、`setup_closed` 各有可恢复的提示；损坏 JSON、HTML、未知枚举、超界列表显示带请求 ID 的安全错误且不进入缓存；写请求网络失败时显示结果未知并提供核对（目录操作可安全重试） |
| A4 | R3 | 同一标签页 A 退出、B 登录后看不到 A 的任何数据；A 的迟到响应和写操作回调不渲染、不导航；另一标签页收到广播或重新可见后同步登录状态 |
| A5 | R6 | 只在运行中且页面可见时轮询，终态即停止；六种状态文案互不混同；不出现百分比或剩余时间 |
| A6 | R4 | 375、768、1024、1280、1440 宽度下各页无遮挡、无整页横向溢出（Playwright 断言）；登录、导航、目录操作、扫描、成员的主要操作只用键盘可完成且焦点可见；对话框有名称，Esc 或取消可关闭，焦点回到触发按钮；axe（WCAG 2.1 A/AA）在登录、概览、扫描目录、扫描详情、成员页没有 serious 或 critical 问题；最小字号 12px；正文与操作文字对比度不低于 4.5:1 |
| A7 | R9 | 样本覆盖清单中的全部端点与错误码；改动后端响应而不更新样本时集成测试失败；样本不含绝对路径、密码、Cookie；每个样本都被契约测试解码 |
| A8 | R1 | 构建只产出 woff2 可变字体（合计约 6.3 MB）并附 OFL 许可文本；页面没有任何第三方请求（Playwright 断言）；字体未加载时系统衬线回退可读 |
| A9 | R10 | 前端作业没有入口缺失分支，执行构建、漂移检查与 Playwright 冒烟；本地按 CI 顺序执行全部通过 |
| A10 | R11 | 文档与实际交付一致，链接检查通过 |

## 实施结果与验收对照（2026-10-01）

| 编号 | 结果 | 证据 |
| --- | --- | --- |
| A1 | 达成 | `package.json` 没有 `latest` 或范围版本；`npm ci`、lint、typecheck、Vitest（29 个文件 443 个测试）、build 通过，构建结果与提交的内嵌产物一致（`git diff --exit-code` 与未跟踪文件检查）；Go 测试 `TestAPIFallbackKeepsFrontendAndHealthRoutes` 与 `/api` 兜底测试覆盖深链回落、缺失的 `/assets/*` 为 404、未知 `/api/*` 为 JSON 404；真实接口冒烟里 `/library`、`/library/scans/{id}`、`/members` 刷新正常 |
| A2 | 达成 | 隔离 PostgreSQL 与临时目录上的真实接口冒烟 82 项通过，覆盖初始化 → 自动登录 → 添加目录 → 开始扫描并看到终态（成功与不完整）与诊断 → 停用并恢复目录 → 新建成员 → 停用并重新启用 → 撤销会话 → 退出；命令与结果见 `research/real-api-smoke.md` |
| A3 | 达成 | 401 回到登录页（Vitest、Playwright、真实冒烟的撤销会话）；403 显示拒绝且会话保留（Playwright）；`revision_conflict`（Vitest、Playwright）、`idempotency_conflict` 与 `setup_closed`（Vitest）、`last_admin` 与 `username_taken`（Playwright、真实冒烟）各有可恢复的提示；损坏 JSON、HTML、未知枚举、超界列表显示带请求 ID 的安全错误且不进入缓存（请求层与 decoder 测试）；写请求网络失败时显示“结果未确认”并提供核对，目录操作用同一个幂等键重试（Vitest 的请求或响应丢失用例） |
| A4 | 达成 | 会话控制器的代次、迟到响应与写操作回调、`BroadcastChannel` 广播与重新可见时核对有 Vitest 覆盖；跨标签页行为另在 Step 3 于真实后端手动验证 |
| A5 | 达成 | 假定时器（Vitest）与 `page.clock`（Playwright）验证只在运行中轮询、终态即停止；六种状态文案互不混同；断言页面没有百分比或剩余时间 |
| A6 | 达成，读屏未人工验证 | Playwright：10 个页面状态在 375 / 768 / 1024 / 1280 / 1440 下页面与主内容区无横向溢出；键盘路径 5 个用例（登录、确认对话框、成员新建、窄屏菜单、跳转链接）；对话框有名称、Esc 与取消可关闭、焦点回到触发按钮；axe（WCAG 2.1 A/AA，含 `color-contrast`）在 16 个页面状态 × 桌面与手机宽度加窄屏菜单下没有 serious 或 critical；所有文字不小于 12px。`e2e/checks.spec.ts` 证明这些检查会失败 |
| A7 | 达成 | 样本覆盖设计第 9 节的清单；手工篡改一个样本字段、或在样本目录多放一个文件，`TestPostgreSQLAPIResponseSamples` 都会失败并给出更新方法，还原后通过；样本断言不含绝对路径、密码、Cookie；每个样本都被前端契约测试解码 |
| A8 | 达成，字体回退未单独验证 | 产物中只有 woff2（112 个，合计约 6.0 MB）并附三套 OFL 文本；页面没有任何第三方请求（Playwright 每个测试断言）；字体栈以系统衬线收尾，字体未加载时的可读性没有单独的测试，留给 M1b 的长标题页面一并验证 |
| A9 | 达成 | CI 前端作业没有入口缺失分支，依次执行 `npm ci`、lint、typecheck、Vitest、构建、漂移检查、安装 Chromium、Playwright；本地按 CI 顺序执行全部通过（本次文档同步提交前复核，另跑 `./scripts/test-integration.sh`） |
| A10 | 达成 | 前端各规范、后端运行合同与质量规范、ADR 0001、Core 1 路线与架构图、架构决策摘要、DESIGN.md、README、父任务与 M1b 的 PRD 已同步；改动文档的相对链接逐个检查 |

范围内的偏离与取舍（开始扫描 200 时不跳转、活动扫描 5 秒刷新、容器查询表格、成员写操作不重试等）记录在 design.md 第 12 节；
发现的后端缺口记录在 design.md 第 13 节与[后端运行合同的技术债](../../spec/backend/core0-runtime-contracts.md)，都不在本任务范围内修复。

## 关键决策（随本规划一并批准）

- **K1 会话安全门禁（ADR 0001 第 4 项）**：会话代次、统一清理、跨标签页广播与重新可见时核对，作为 M1a 的验收门禁；
  API 请求一律 `no-store`。封面的 HTTP 缓存头属于后端行为，M1a 不改，M1b 显示封面时再评估。
- **K2 依赖、浏览器与重试（ADR 0001 第 5 项）**：直接依赖精确固定。支持 2023 年以后的常青浏览器：Chrome / Edge 111+、
  Firefox 113+、Safari 与 iOS Safari 16.4+（`color-mix()` 等特性需要）；自动化只跑 Chromium。查询只对网络失败与 503
  重试，最多 2 次；4xx 与无效响应不重试；写操作从不自动重试；窗口重新聚焦时刷新过期数据（默认 30 秒过期）。
- **K3 字体**：用 Fontsource 可变字体包（只有 woff2，一个文件覆盖 400 与 600），约 6.3 MB 进入仓库与二进制；
  静态包会连同 woff 产出约 22 MB，不采用。按原样再分发包内文件并附 OFL 文本。
- **K4 测试工具**：Testing Library 做组件交互测试；Playwright 加 axe-core 做浏览器冒烟与无障碍检查，CI 只装 Chromium
  （预计每次多 1–2 分钟）。不引入 MSW、jest-dom 等其他测试库。
- **K5 URL 形状**：`/`、`/setup`、`/login?redirect=`、`/library`、`/library/scans/$scanId`、`/members`；旧 hash 链接与
  `?release=` 不做兼容。
- **K6 视觉基线（DESIGN 第 10 节第 2 项）**：最小字号 12px，正文 15px，控件与表格 14px，中文段落行高 1.6–1.75；断点
  ≥1200 / 768–1199 / <768；小字与链接用 `accent-700`，`accent` 只用于描边与细线；焦点环为 2px `accent-700`。
- **K7 样本位置**：`backend/internal/app/testdata/api-samples/`；Go 测试逐字比较，`ROOMUSIC_UPDATE_API_SAMPLES=1` 时重写；
  漂移由集成作业把关，前端作业读取样本。
- **K8 范围细节**：包含成员重新启用、目录操作记录、初始化后自动登录；概览计数使用 `/release-groups`，其 decoder 在 M1a
  实现、M1b 复用。

## 范围外

- 专辑列表与详情（M1b）；播放与播放条（M2a）；元数据修正与审批（M3）；个人库（P、M5）；Steward（M6）。
- 后端接口与行为变更：扫描历史列表、创建管理员或修改角色、成员操作幂等、成员列表分页、允许根目录查询、封面缓存头。
  界面按现有接口如实呈现，缺口记入后续任务。
- 旧 hash 链接兼容；任何测试使用真实音乐目录；拆分 `core0-runtime-contracts.md`（另行处理）。

## 风险

- TypeScript 6 与 TanStack Router 的类型、`exactOptionalPropertyTypes` 可能不兼容：第一步就验证；不兼容时换用可用的
  精确版本，或只关闭被阻断的单个选项并记录原因。
- 中文字体加载与布局位移：保留系统回退与 `font-display: swap`；位移只观察记录，不承诺为零。
- 会话清理的竞态：代次守卫、重新挂载、查询键带代次三重保护，用测试覆盖迟到响应。
- CI 时长增加（浏览器下载约 1–2 分钟）。
- 约 6.3 MB 字体二进制进入 Git 历史，推送后无法撤出。
- 小字号统一提到 12px 后与原型的视觉差异。

## 验证方式

- 前端：`npm ci`、`npm run lint`、`npm run typecheck`、`npm run test`、`npm run build` 后检查内嵌资产无漂移；
  `npx playwright install chromium` 后 `npm run test:e2e`。
- 后端只新增测试与 testdata：`gofmt -l .`、`go vet ./...`、`go test ./internal/app/... -count=1`、
  `./scripts/test-integration.sh`（含样本比较）。后端生产代码不变，不跑全量 `-race`。
- 真实接口主路径：隔离 PostgreSQL 与临时目录上的一次冒烟，命令与结果记录在任务 `research/`。
