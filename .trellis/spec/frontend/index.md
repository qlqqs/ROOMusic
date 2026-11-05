# Frontend Development Guidelines

## Status

2026-10-01：M1a 已实现并验证——应用外壳、初始化与会话、目录与扫描管理、成员管理、简版概览。`frontend/src/`
按本层规范重建：React 19 与严格 TypeScript、TanStack Router（代码定义路由，不用 loader）与 TanStack Query
（服务器状态的唯一缓存）、CSS Modules 加集中的 Classical 全局变量、Fontsource 自托管可变字体；直接依赖全部精确固定。
生产页面由 Go 同源提供，构建产物（`backend/cmd/roomusic/web`）随源码提交，CI 检查漂移。
2026-10-03：M1b 在这个外壳上增加了专辑浏览：`/albums`（搜索、需要关注筛选、分页、封面卡片）与 `/albums/$groupId`（头部、版本导航、介质与曲目、发行信息、
折叠的来源与证据，管理员可按需读完整证据），详见[状态](./state-management.md)、[组件](./component-guidelines.md)与[质量](./quality-guidelines.md)规范。

M2a 的播放器入口与生命周期见[状态规范](./state-management.md)、[播放器规范](./player-design-guidelines.md)及
[后端播放合同](../backend/playback-contracts.md)。直出仅传原字节；M2b 增加 ALAC 的服务端无损派生与内嵌歌词（见[歌词合同](../backend/lyrics-contracts.md)），阶段 P 增加按用户隔离的服务端播放历史与概览的“最近播放”（见[播放历史合同](../backend/listening-contracts.md)）；
自动化与三组手工实播分别记录，不把浏览器能力提示当作验收证据。

本轮 Classical 的唯一落地设计锚为 [frontend/DESIGN.md](../../../frontend/DESIGN.md)，
来源是 `frontend-desgin/` 中的原型。旧月刊布局、hash 路由、模拟播放器和生产演示入口
不再是默认要求；仍适用的队列语义与安全边界保留在本层规范。

2026-10：M3 增加管理员的受治理写操作（`features/governance`）：专辑详情里编辑发行信息、调整归组与撤销，“资料库”下的“操作记录”页与详情里的“修改记录”；
普通成员没有编辑入口，只看到有效值与“人工修正”标记（后端合同见[受治理操作合同](../backend/governance-contracts.md)）。
M4 的第一项增加默认版本（管理员在专辑详情设定、可撤销）与版本对比（所有成员，专辑详情的“对比版本”），合同见[默认版本与版本对比合同](../backend/default-version-contracts.md)；
第二项增加管理员的“元数据问题”页（`/issues`：按版本列出问题，核对、取消核对与撤销），合同见[元数据问题与核对合同](../backend/issue-review-contracts.md)；第三项增加管理员的“待认领”页（`/claims`：把已不可见的旧版本上的修改认领给新版本，可撤销），合同见[认领合同](../backend/claim-contracts.md)。依赖与路由方案的决策见
[前端基础 ADR](../../../docs/architecture/adr/0001-frontend-foundations.md) 决策记录，阶段和
能力矩阵见 [Core 1 路线](../../../docs/architecture/core-1-roadmap.md)，M1a 的设计与验证记录见任务
[M1a 技术设计](../../tasks/archive/2026-10/09-28-classical-m1a-shell-admin/design.md)。各规范文件区分“已实现的合同”与尚未实现的目标
（播放器、搜索、受治理操作与 Agent 界面）；后者不是当前前端已通过验收的声明。

## Pre-Development Checklist

Before writing or reviewing frontend code:

- [ ] Read the shared [guides index](../guides/index.md), then read
      [Product Goals](../guides/product-goals.md),
      [Modular Design](../guides/modular-design.md), and
      [Cross-Layer Thinking](../guides/cross-layer-thinking-guide.md).
- [ ] Read the layer guides below that govern the change. Read component,
      hook, state, and type rules for a workflow change; read the quality guide
      for every implementation task.
- [ ] Identify the owning feature, its typed REST contract, and the natural
      owner of server, URL, form, and local UI state.
- [ ] Confirm the proposed UI preserves backend authority and does not make
      GraphQL, Agent, plugin, playback, PWA, Redis, or Meilisearch behavior a
      Core 0 prerequisite.

## Layer Guides

| Guide | Contract |
| --- | --- |
| [Classical 设计依据](../../../frontend/DESIGN.md) | 原型来源、设计变量、布局（第 1–4 节）；组件状态、播放与页面能力见 [DESIGN-components.md](../../../frontend/DESIGN-components.md)（第 5–7 节），落地差异与验收见 [DESIGN-acceptance.md](../../../frontend/DESIGN-acceptance.md)（第 8–10 节） |
| [Core 1 路线](../../../docs/architecture/core-1-roadmap.md) | 当前能力、已批准阶段与依赖、ADR 裁决状态、旧任务承接与实施门禁 |
| [Directory Structure](./directory-structure.md) | App composition, feature ownership, public boundaries, and shared code |
| [Component Guidelines](./component-guidelines.md) | Presentational contracts, workflow states, accessibility, and composition |
| [Hook Guidelines](./hook-guidelines.md) | Stateful coordination, REST access, cancellation, and polling |
| [State Management](./state-management.md) | Server, URL, session, and local state ownership |
| [Type Safety](./type-safety.md) | Strict TypeScript, DTO decoding, and exhaustive domain states |
| [Quality Guidelines](./quality-guidelines.md) | Tests, accessibility, gates, and forbidden shortcuts |
| [前端验证分层](./quality-verification-layers.md) | 各阶段（M2a 播放、M3、M4 三项）的测试分层：最小 Vitest 命令、共用接口行为、页面级、浏览器冒烟、真实服务用例 |
| [受治理操作的组件约定](./governance-components.md) | 编辑发行信息、归组、默认版本、元数据问题页、待认领页与操作记录的组件约定 |
| [艺人与别名的前端约定](./artist-guidelines.md) | M5 ⑤：`features/artists` 的归属、可点的艺人名（`ArtistNames` 与就绪判断）、艺人列表与艺人页的地址与查询键、艺人收藏的分开读取、整理中的处理、合并/拆开/改名与候选的界面 |
| [收藏与歌单的前端约定](./personal-guidelines.md) | M5 ② ③：`features/personal` 的归属、查询键与跨标签页刷新、心形与批量状态、收藏页的保留行、歌单的幂等命令、拖动与菜单排序、撤销、“添加到歌单…”与统一曲目行动作 |
| [播放器设计规范](./player-design-guidelines.md) | 工作台视觉层级、响应式布局、播放交互与可访问性 |
| [Core 0 当前运行合同](../backend/core0-runtime-contracts.md) | 平台层 REST 通则、HTTP 缓存、环境、CI 与专题合同索引 |
| [身份与成员合同](../backend/identity-contracts.md) | 查看者响应、密码规则、成员与初始化接口的错误映射 |
| [目录与扫描流程合同](../backend/library-scan-contracts.md) | 目录接口、目录幂等、扫描整理与封面发现 |
| [扫描协调合同](../backend/scan-coordination-contracts.md) | 扫描状态、扫描记录、持久化取消与 `ScanDTO` |
| [Catalog REST 合同](../backend/catalog-rest-contracts.md) | 发行与按组接口、封面摘要、可见性与错误合同 |
| [受治理操作合同](../backend/governance-contracts.md) | M3 的修正、归组、撤销、操作记录接口与错误码，前端命令与界面据此 |
| [默认版本与版本对比合同](../backend/default-version-contracts.md) | M4 的默认版本（设定、生效规则、撤销）与版本对比接口 |
| [元数据问题与核对合同](../backend/issue-review-contracts.md) | M4 的问题列表、版本的问题、核对与撤销接口，“需要关注”的三类拆分 |
| [认领合同](../backend/claim-contracts.md) | M4 第三项：待认领的判定与推荐、预览与令牌、认领与撤销、读取接口 |

## Quality Check

Before declaring frontend work complete:

- [ ] 按[前端质量规范](./quality-guidelines.md)运行适用门禁：lint、类型检查、Vitest、生产构建与内嵌资产漂移检查；
      涉及页面与交互时运行 Playwright 冒烟（`npm run test:e2e`），仅文档变更不必运行。
- [ ] Verify raw REST payloads are decoded once at the feature API boundary,
      types are exhaustive, and state has one clear owner.
- [ ] Verify loading, empty, stale, conflict, canceled, and error states that
      apply to the workflow are visible and recoverable.
- [ ] Verify semantic HTML, keyboard interaction, labels, focus behavior, and
      narrow and desktop layouts for touched screens.
- [ ] Confirm cookie/session, role, path, revision, and operation checks remain
      backend authority; update a linked guideline when a Core 0 contract or
      selected implementation tool changes.

## Core 0 前端保留合同（M1a 已按此实现）

- React/TypeScript runs through a development server for local iteration.
  Production assets are built once and served by the Go application from the
  same origin as `/api/v1`; Core 0 has no separate production frontend service.
- The API boundary sends and decodes versioned REST. Core 0 does not add GraphQL
  clients, subscriptions, or generated GraphQL types.
- Authentication is an opaque server-side session in an HttpOnly cookie. Browser
  code never reads, stores, or refreshes a bearer token. State-changing requests
  follow the backend's same-origin/CSRF contract.
- UI role checks control navigation and disabled states only. Every backend
  request still enforces session, role, resource, path, revision, and operation
  authority.
- Core screens cover initialization/login as applicable, library browse, Release
  detail with Medium/Track structure, basic search, directory/scan administration,
  and loading, empty, stale, conflict, canceled, and error states. M1a 已交付初始化与登录、
  概览、目录与扫描管理、成员管理；M1b 已交付专辑浏览、详情与基础搜索。
- The UI displays safe resource identities and evidence. It never receives or
  constructs arbitrary host filesystem paths for ordinary users.
- Redis, Meilisearch, Agent runtime, runtime plugins, playback, PWA offline
  behavior, and file mutation are not Core 0 frontend prerequisites.

## Default Data Flow

```text
route/page
  -> feature component
  -> feature hook/use case
  -> feature API client + one runtime decoder
  -> shared HTTP transport
  -> /api/v1
```

Responses flow back as typed DTOs and feature-owned view models. Components do
not call `fetch` directly, cast raw JSON, query a global store for unrelated
state, or recreate backend policy.

## User Experience Principle

ROOMusic is a work-focused library management interface for private collections.
Prefer dense, scannable Release/Medium/Track information, predictable navigation,
explicit operation status, and recoverable errors. Do not turn administration or
browse flows into marketing-style pages.

播放器工作流的具体视觉、布局和交互约束见[播放器管理界面设计规范](./player-design-guidelines.md)。

## Evidence

- [Core 0 UI acceptance behavior](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md)
- [Current development environment](../../../README.md)
- [Inherited product audience and long-term direction](../guides/product-goals.md)
