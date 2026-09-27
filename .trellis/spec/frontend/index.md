# Frontend Development Guidelines

## Status

2026-09-27 基线：`frontend/src/` 已删除，尚未恢复客户端。`frontend/` 保留 React、
TypeScript、Vite、ESLint、Vitest/jsdom、lucide-react 的依赖声明与配置；这不等于相应
组件、decoder 或测试当前存在。生产内嵌页面为前端重置占位页，CI 可跳过真实前端构建。

本轮 Classical 的唯一落地设计锚为 [frontend/DESIGN.md](../../../frontend/DESIGN.md)，
来源是 `frontend-desgin/` 中的原型。旧月刊布局、hash 路由、模拟播放器和生产演示入口
不再是恢复客户端的默认要求；仍适用的队列语义与安全边界保留在本层规范。

M0 仅获批整理文档。M1 的候选范围是用现有 API 恢复客户端，既有初始化、会话、目录、
扫描和成员管理写操作仍可使用；不新增元数据治理写路径。新依赖与路由方案见
[前端基础 ADR 草案](../../../docs/architecture/adr/0001-frontend-foundations.md)，阶段和
能力矩阵见 [Core 1 路线提案](../../../docs/architecture/core-1-roadmap.md)。草案不授权安装依赖。
以下是后续恢复实现需遵守的合同，不是当前前端已通过功能验收的声明。

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
| [Classical 设计依据](../../../frontend/DESIGN.md) | 原型来源、设计变量、布局、组件状态和落地差异 |
| [Core 1 路线提案](../../../docs/architecture/core-1-roadmap.md) | 当前能力、待批准阶段、旧任务承接与实施门禁 |
| [Directory Structure](./directory-structure.md) | App composition, feature ownership, public boundaries, and shared code |
| [Component Guidelines](./component-guidelines.md) | Presentational contracts, workflow states, accessibility, and composition |
| [Hook Guidelines](./hook-guidelines.md) | Stateful coordination, REST access, cancellation, and polling |
| [State Management](./state-management.md) | Server, URL, session, and local state ownership |
| [Type Safety](./type-safety.md) | Strict TypeScript, DTO decoding, and exhaustive domain states |
| [Quality Guidelines](./quality-guidelines.md) | Tests, accessibility, gates, and forbidden shortcuts |
| [播放器设计规范](./player-design-guidelines.md) | 工作台视觉层级、响应式布局、播放交互与可访问性 |
| [Core 0 当前运行合同](../backend/core0-runtime-contracts.md) | REST、环境、扫描、事务与跨层安全合同 |

## Quality Check

Before declaring frontend work complete:

- [ ] 按[前端质量规范](./quality-guidelines.md)运行适用门禁；当前脚本已有配置，
      但源码缺失时的脚本成功不等于客户端验收。M0 只验证文档，M1 须验证真实构建与行为。
- [ ] Verify raw REST payloads are decoded once at the feature API boundary,
      types are exhaustive, and state has one clear owner.
- [ ] Verify loading, empty, stale, conflict, canceled, and error states that
      apply to the workflow are visible and recoverable.
- [ ] Verify semantic HTML, keyboard interaction, labels, focus behavior, and
      narrow and desktop layouts for touched screens.
- [ ] Confirm cookie/session, role, path, revision, and operation checks remain
      backend authority; update a linked guideline when a Core 0 contract or
      selected implementation tool changes.

## Core 0 前端保留合同（恢复实现时适用）

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
  and loading, empty, stale, conflict, canceled, and error states.
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
