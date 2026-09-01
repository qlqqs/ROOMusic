# Frontend Development Guidelines

## Status

The repository does not yet contain a React/TypeScript application. These files
define planned Core 0 contracts rather than describing implemented components or
selected libraries. The first frontend scaffold must record its package manager,
build tool, router, data-fetching/cache mechanism, runtime decoder, styling
approach, test runner, and exact quality commands.

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

## Quality Check

Before declaring frontend work complete:

- [ ] Follow the [Frontend Quality Guidelines](./quality-guidelines.md) and run
      every applicable, selected repository gate; until the frontend scaffold
      exists, record the missing command rather than inventing one.
- [ ] Verify raw REST payloads are decoded once at the feature API boundary,
      types are exhaustive, and state has one clear owner.
- [ ] Verify loading, empty, stale, conflict, cancelled, and error states that
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
  and loading, empty, stale, conflict, cancelled, and error states.
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

## Evidence

- [Core 0 UI acceptance behavior](../../tasks/08-31-roomusic-core-0-rebuild/prd.md)
- [Current development environment](../../../README.md)
- [V0 product audience and long-term direction](../../../../ROOMusic-V0/.planning/PROJECT.md)
