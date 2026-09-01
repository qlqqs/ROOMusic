# ROOMusic Architecture Decisions

## Status

本文档是供 Trellis 任务上下文读取的架构摘要。此目录中的 Canvas 文件提供
可视化细节；本文档记录实现和审查必须遵守的长期规则。

## 文档语言

项目新增或修改的文档必须使用简体中文。范围包括 README、架构文档、ADR、
API 文档、Trellis 任务文档、说明性注释和运维文档。代码标识符、文件名、命令、
协议名称、产品名称和必须保留的原文引用可以使用英文。与当前任务无关的已有
英文内容不需要翻译，除非用户明确要求。

## System shape

- ROOMusic is a modular monolith: one Go backend process, a React/TypeScript
  development frontend, and PostgreSQL as the required business authority.
- Production Web UI and `/api/v1` REST are served from the same Go application.
- Redis, Meilisearch, GraphQL, playback, PWA offline behavior, and external
  production frontend services are optional future work, not Core 0 runtime
  dependencies.
- The repository tree expresses large capability boundaries. Concrete Go and
  TypeScript files are chosen from the actual use cases and may evolve within
  their owning module.

## Core 0 当前实现树

当前可运行代码仍采用过渡单体结构：

```text
backend/cmd/roomusic/*.go   # HTTP、身份、扫描、目录和 catalog 过渡实现
backend/migrations/*.sql    # embed.FS 按序执行的 SQL
frontend/src/main.tsx       # React 页面与本地状态编排
frontend/src/api.ts         # REST DTO、decoder 和 transport
frontend/src/styles.css     # 工作台样式与响应式布局
```

下方 `backend/internal/*` 与 `frontend/src/features/*` 仅是目标能力边界。
在第二个真实消费者、路由边界或独立生命周期形成前，不为目录对称性进行
大规模拆分。

## Repository-level modules

```text
backend/       Go modular monolith and backend-owned migrations
frontend/      React/TypeScript Web UI
deploy/        Compose, containers, and production setup
extensions/    Stable typed capability contracts for future extensions
plugins/       Future concrete plugin implementations
docs/          Architecture, API, ADR, security, and operations documentation
scripts/       Development, database, fixture, and verification helpers
```

`backend/migrations/` is the canonical migration location. Do not create a
second root-level `migrations/` directory unless an explicit design decision
changes the migration owner.

## Backend capability ownership

```text
backend/internal/
├── app/          composition root, route mounting, lifecycle
├── platform/     config, HTTP mechanics, PostgreSQL, clock, observability
├── identity/     setup, users, sessions, roles, authentication authority
├── library/      allowed roots, filesystem safety, scanner, parsers, diagnostics
├── catalog/      ReleaseGroup, Release, Medium, Track, provenance, read model
├── search/       query semantics and rebuildable search projections
├── operations/   Change Set, Operation Journal, revision, idempotency, recovery
└── agent/        Assistant/Steward/Operator modes, plans, review, tool authority
```

Only the first five modules are part of the current first-browse slice. The
remaining modules are planned boundaries, not a reason to create empty packages.

Each capability owns its policy, application use cases, ports, adapters, and
tests. Cross-capability calls use published typed contracts or narrow
consumer-owned ports. A capability must not import another capability's private
adapter, transport, database row type, table, or helper.

## Frontend capability ownership

```text
frontend/src/
├── app/             routes, providers, authenticated shell
├── features/auth/   setup, login, session, logout
├── features/library/ roots, scans, diagnostics
├── features/catalog/ releases, media, tracks, artwork presentation
├── features/search/ query, filters, results
├── features/operations/ status, history, recovery UI
├── features/agent/  Assistant/Steward/Operator UI
└── shared/          HTTP transport, policy-free UI, neutral mechanics
```

Core 0 creates `app`, `auth`, `library`, `catalog`, and the required `shared`
parts. Feature code owns its REST DTOs, runtime decoders, hooks, view models,
components, and routes. `shared` must never import `features` or `app`.

The frontend flow is:

```text
route → feature component → feature hook → feature API/decoder
      → shared HTTP transport → /api/v1
```

Browser code uses the HttpOnly session cookie and never reads or stores a bearer
token. Frontend role checks only control presentation; backend authorization is
the authority.

## Dependency direction

```text
HTTP transport → application use case → domain + consumed ports
                                             ↑
                              PostgreSQL/filesystem adapters
```

The composition root creates concrete adapters and injects them into application
services. Domain code stays deterministic where possible. Transport decodes and
maps; application code coordinates authority and business rules; adapters
enforce physical storage constraints.

The library scanner publishes observations through a narrow catalog contract. It
does not write catalog-private tables or decide Release Graph invariants.

## Core safety invariants

- Setup atomically creates one initial administrator and then closes public setup.
- Sessions use high-entropy opaque tokens; PostgreSQL stores only token hashes,
  expiry, and revocation state.
- Library roots are canonicalized and checked with realpath containment against
  configured `allowed_library_roots`.
- Directory symlinks are not followed by default. File symlinks are readable only
  when their resolved target remains within the same allowed root.
- The music library is read-only. Core code never modifies, moves, deletes, or
  writes tags to source files.
- Only a complete successful scan may perform negative `missing` reconciliation.
- PostgreSQL remains authoritative; any future search index or provider output is
  a rebuildable projection, candidate, observation, or explicitly authorized
  execution result.
- Ordinary REST responses and logs must not expose passwords, tokens, database
  URLs, allowlists, or unnecessary absolute music paths.

## Extension boundary

`extensions/` describes typed capability contracts. `plugins/` contains future
implementations such as additional parsers, metadata providers, artwork
providers, agent providers, or execution providers. A plugin cannot grant itself
capabilities, bypass backend authority, or mutate core state directly.

Capability Registry and Plugin Host are future runtime infrastructure. Do not
introduce a service locator, dynamic plugin loader, RPC layer, or WASM runtime
until an approved feature requires that boundary.

## Source documents

- `docs/architecture/roomusic-modular-plugin-architecture.canvas.tsx`
- `docs/architecture/roomusic-full-architecture-tree.canvas.tsx`
- `docs/architecture/core-0-file-structure.canvas.tsx`
- `.trellis/spec/guides/modular-design.md`
- `.trellis/spec/backend/directory-structure.md`
- `.trellis/spec/frontend/directory-structure.md`
