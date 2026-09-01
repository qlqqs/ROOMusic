# Frontend Development Guidelines

## Status

当前 Core 0 已有 React + TypeScript + Vite 前端，代码暂集中在
`frontend/src/main.tsx`、`frontend/src/api.ts` 和 `frontend/src/styles.css`。
请求使用原生 `fetch`/Cookie credentials，响应由手写 runtime decoder 校验，
测试使用 Vitest，静态检查使用 ESLint；尚未引入 router、query/cache 或 UI 库。
目标 feature 分层仍是后续演进方向，不代表当前目录已经存在。

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

- [ ] Follow the [Frontend Quality Guidelines](./quality-guidelines.md) and run
      every applicable, selected repository gate; until the frontend scaffold
      exists, record the missing command rather than inventing one.
- [ ] Verify raw REST payloads are decoded once at the feature API boundary,
      types are exhaustive, and state has one clear owner.
- [ ] Verify loading, empty, stale, conflict, canceled, and error states that
      apply to the workflow are visible and recoverable.
- [ ] Verify semantic HTML, keyboard interaction, labels, focus behavior, and
      narrow and desktop layouts for touched screens.
- [ ] Confirm cookie/session, role, path, revision, and operation checks remain
      backend authority; update a linked guideline when a Core 0 contract or
      selected implementation tool changes.

## Core 0 Frontend Contract

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
