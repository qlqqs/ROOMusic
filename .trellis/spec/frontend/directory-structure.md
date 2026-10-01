# Frontend Directory Structure

## 现状与意图

2026-10-01 的 M1a 已按下面的结构重建 `frontend/src/`。目录只为获批页面和真实消费者建立，不预建空文件夹；
`player`、`curation`、通用 `operations`、`artists`、`personal`、`steward` 随对应获批阶段建立，基础 Release 搜索由
`catalog` 拥有，不为它新增搜索服务。阶段命名和依赖见 [Core 1 路线](../../../docs/architecture/core-1-roadmap.md)。
设计变量集中在 `styles/`，不复制设计工具的运行时。

## 实际布局

```text
frontend/
├── e2e/                            # Playwright 冒烟；受控后端在 e2e/support/api-mock.ts，响应取自后端样本
├── playwright.config.ts
├── public/licenses/                # 三套字体的 OFL 文本，构建时复制到产物根目录
└── src/
    ├── main.tsx
    ├── app/
    │   ├── providers.tsx           # QueryClient、会话、Router 的装配
    │   ├── runtime.ts              # createAppRuntime：缓存、会话控制器、路由与清理的唯一接线
    │   ├── router.tsx              # 路由树与守卫
    │   ├── shell/                  # 外壳、侧栏、窄屏导航、身份区、导航项
    │   ├── routes/                 # 页面组合与整页状态（登录、无权限、不存在、服务不可用）
    │   └── *.test.tsx              # 页面级测试：经应用运行时与替身后端验证整页行为
    ├── features/
    │   ├── auth/                   # 初始化、登录、退出、会话生命周期与跨标签页同步
    │   ├── members/                # 成员列表与管理（管理其他账号，生命周期与 auth 不同）
    │   ├── library/                # 目录、目录操作记录、扫描与诊断
    │   └── catalog/                # 专辑列表合同；M1a 只用于概览计数，M1b 扩展为浏览
    ├── shared/
    │   ├── api/                    # 请求、错误分类、解码原语、错误信封、重试策略、失败文案
    │   ├── ui/                     # 无策略的表现组件
    │   └── lib/                    # 与领域无关的小机制：幂等键与请求 ID、时间、数字、类名、页面标题
    ├── styles/                     # tokens.css、base.css、fonts.ts
    └── test/                       # 测试共用基础设施：渲染、替身后端、样本读取、受控通道
```

feature 只建需要的子目录；文件名统一小写短横线，组件以 PascalCase 导出，样式模块与组件同目录（`*.module.css`）：

```text
features/library/
├── api/                            # DTO、decoder、endpoint（decoders.ts、endpoints.ts）
├── components/                     # 组件与它们的样式、文案映射（root-messages.ts）
├── hooks/                          # 查询与命令 hook
├── query-keys.ts                   # 带查看者代次的查询键
├── scan-status.ts                  # 纯函数的视图投影与文案映射放在 feature 根目录的小模块
├── index.ts                        # 窄公开入口：只导出 app 组合页面所需的内容
└── *.test.ts(x)
```

测试与被测单元同目录。跨页面的整页行为（路由、会话、替身后端）放在 `app/*.test.tsx`；测试共用的替身后端与读取
后端样本的代码放在 `src/test/`，它们依赖 vitest，不被 Playwright 复用。浏览器冒烟放在 `frontend/e2e/`。

## 归属

- `app` 组合路由、顶层 provider、会话启动与外壳，不吸收 feature 策略。`runtime.ts` 是缓存、会话与路由之间唯一的接线点。
- `auth` 拥有初始化与登录表单、会话控制器、退出、当前身份的展示与角色标签；服务器仍是认证权威。管理**其他**账号不属于
  `auth`，由 `members` 拥有。
- `members` 拥有成员列表、新建、停用、重新启用与撤销会话。
- `library` 拥有目录与扫描管理、目录操作记录、扫描详情与诊断的展示。
- `catalog` 拥有 Release/ReleaseGroup 的列表与详情、Medium/Track 展示、来源与封面展示；M1a 只有概览的计数。
- `search` 拥有查询与筛选的 URL 状态和结果展示（尚未建立）。
- `operations` 拥有 Change Set 与 Operation Journal 的状态及受支持的恢复界面，不是通用 toast（尚未建立）。
- `shared/api` 拥有同源请求、凭据、请求关联、响应体处理、错误信封、重试策略与解码原语；feature 的端点与 DTO 留在 feature。
  `fetch` 只在 `shared/api/request.ts` 中调用，由 ESLint 规则保证。
- `shared/ui` 放至少两处使用的、无策略的表现组件：不发请求、不判断角色、不引用 feature。

## Import Rules

- `app` may compose feature public surfaces.
- Features may import `shared` and a deliberately published contract from
  another feature. They do not import another feature's private component, hook,
  API endpoint, or state.
- `shared` never imports `features` or `app`.
- Feature-to-feature cycles are forbidden. Move orchestration to `app` or
  publish a smaller typed contract.
- Avoid deep relative imports across boundaries. A public entry is narrow; it is
  not a barrel that re-exports the whole feature.

## Naming

- React components and their exported types use `PascalCase`; hooks start with
  `use`; functions/values use `camelCase`.
- 文件名统一小写短横线（`root-table.tsx`、`use-scan.ts`）；路由与 feature 目录名使用产品词汇，不用后端表名。
- Wire types end in `DTO` when distinction from a view/domain model matters.
  Runtime decoders use verbs such as `decodeReleaseDetail`.
- Tests use `*.test.ts` or `*.test.tsx` beside the owner. Do not create a
  repository-wide `__tests__` mirror tree.

## Example Placement

A Release page route composes `ReleaseDetail` and its feature hook. The hook
calls an endpoint function in `catalog/api/endpoints.ts`, which uses the shared
HTTP transport and a decoder from `catalog/api/decoders.ts`. A reusable skeleton
may move to `shared/ui` only when another implemented feature needs the same
presentation contract（`DataTable` 就是这样在目录、操作记录、诊断与成员列表都需要时才进入 `shared/ui` 的）。

The page never imports a PostgreSQL-shaped row, exposes an absolute artwork path,
or combines a scan mutation into catalog display merely because both mention a
Release.

## Production Boundary

The frontend build output is embedded or served by the Go application in
production. Do not add a production Node server, cross-origin authentication
scheme, or CORS dependency for Core 0. Local development may use a dev-server
proxy to the Go REST API.

## Anti-Patterns

- Global `components`, `hooks`, `services`, or `types` buckets containing
  unrelated feature policy.
- One `api.ts` file with every endpoint and DTO.
- Importing private feature state to avoid defining a narrow contract.
- A generic `utils.ts` that accumulates path, permission, date, and Release
  rules.
- Creating player, Agent chat, plugin marketplace, or Meilisearch client folders
  before those phases are approved.

This structure mirrors backend capability ownership in
[Modular Design](../guides/modular-design.md) and the current
[Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
