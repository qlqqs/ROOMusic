# Backend Development Guidelines

## Status

当前仓库已有 Go Core 0 应用，入口位于 `backend/cmd/roomusic`，使用
`net/http`、`database/sql + pgx/v5`、`embed.FS` 迁移、bcrypt 和 `log/slog`。
代码仍是过渡单体；下文的 `internal/*` 分层是目标架构，只有在真实边界形成后
才拆分。

Core 0 is a modular monolith: one Go application, versioned REST, in-process scan
coordination, PostgreSQL as the only required business authority, and a read-only
music library.

## Pre-Development Checklist

Before writing or reviewing backend code:

- [ ] Read the shared [guides index](../guides/index.md), then read
      [Product Goals](../guides/product-goals.md),
      [Modular Design](../guides/modular-design.md), and
      [Engineering Principles](../guides/engineering-principles.md).
- [ ] Read the layer guides below that govern the change. Read database,
      error, and logging rules whenever a backend workflow crosses those
      boundaries; read the Music Steward/operation guide for any Agent, tool,
      persistent operation, approval, or recovery work; read the quality guide
      for every implementation task.
- [ ] Identify the owning capability, its published contract, the input
      boundary, and the backend authority for the requested behavior.
- [ ] Confirm the change fits the Core 0 PostgreSQL-only, versioned REST,
      opaque-session, read-only-library scope before introducing an adapter or
      dependency.

## Layer Guides

| Guide | Contract |
| --- | --- |
| [Directory Structure](./directory-structure.md) | Capability ownership, package layout, and dependency direction |
| [Database Guidelines](./database-guidelines.md) | PostgreSQL ownership, transactions, migrations, revisions, and idempotency |
| [Error Handling](./error-handling.md) | Classified application errors and stable REST responses |
| [Logging Guidelines](./logging-guidelines.md) | Structured JSON events, correlation, and redaction |
| [Core 0 当前运行合同](./core0-runtime-contracts.md) | 当前 REST、环境、事务、扫描和跨层安全合同 |
| [Catalog REST 合同](./catalog-rest-contracts.md) | Release 列表/详情、封面摘要、可见性和错误合同 |
| [Music Steward And Operation Guidelines](./agent-and-operation-guidelines.md) | Agent modes, Review Subagent, tools, Change Sets, and recovery |
| [Quality Guidelines](./quality-guidelines.md) | Focused code, forbidden coupling, tests, and gates |

## Quality Check

Before declaring backend work complete:

- [ ] Follow the [Backend Quality Guidelines](./quality-guidelines.md) and run
      every applicable, selected repository gate; until the Go scaffold exists,
      record the missing command rather than inventing one.
- [ ] Verify the behavior is owned by one capability, ports remain narrow, and
      no private table, adapter, or helper became a cross-module API.
- [ ] Verify request decoding, authorization, path safety, transaction,
      revision/idempotency, classified error, correlation, and redaction
      responsibilities remain at their correct boundaries.
- [ ] Test the representative success path and the relevant dangerous failure
      path, including cancellation or incomplete scans when applicable.
- [ ] Update this spec or a linked guideline when a Core 0 contract or selected
      implementation tool changes.

## Core 0 Boundaries

- Transport decodes requests and maps responses. It does not own business policy
  or issue SQL.
- Application services authorize and coordinate use cases. Domain code contains
  deterministic invariants. Consumer-owned ports describe needed effects.
- PostgreSQL and filesystem packages implement ports. They never call back into
  HTTP or invent domain behavior.
- Identity, library/scanner, catalog, search, and operations capabilities may
  consume another capability's published contract only. Private tables,
  packages, and helpers are not integration APIs.
- Setup is closed after the first administrator. Authentication uses random
  opaque tokens in HttpOnly cookies; PostgreSQL stores only token hashes and
  revocation state.
- Every protected backend endpoint performs authorization. Frontend route guards
  are not an authority boundary.
- Library root input is canonicalized and checked against configured
  `allowed_library_roots`. Core 0 does not follow directory symlinks by default
  and never modifies source music.
- Only a complete successful scan can mark absent sources `missing`.
- User-initiated persistent management changes use Change Set and Operation
  Journal boundaries with revision and idempotency protection.
- Redis, Meilisearch, GraphQL, Agent runtime, runtime plugins, playback, and file
  mutation are future work and cannot become hidden Core 0 dependencies.

## Planned Request Shape

A protected write follows this flow:

```text
REST handler
  -> session + CSRF/origin + role middleware
  -> typed application command
  -> domain rules and narrow ports
  -> PostgreSQL transaction / constrained filesystem adapter
  -> typed result
  -> REST presenter
```

An anti-pattern is a handler that trusts a UI-supplied role, canonicalizes a path
itself, runs a repository update, and returns the database row directly.

## Source Evidence

- [Core 0 requirements](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md)
- [Current environment README](../../../README.md)
- [Current Compose services](../../../compose.yaml)
- [Architecture canvas](../../../docs/architecture/roomusic-modular-plugin-architecture.canvas.tsx)

The Compose file includes Redis and Meilisearch as development/future services;
the Core 0 PRD explicitly makes them optional.
