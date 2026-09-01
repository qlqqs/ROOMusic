# Backend Directory Structure

## Status And Intent

当前 Go module 位于 `backend/`，Core 0 实际代码暂集中在
`backend/cmd/roomusic/*.go`，迁移位于 `backend/migrations/`。下方按能力分层的
结构是目标约定；实现尚未满足拆分触发条件时不要创建空目录或伪接口。

## Planned Layout

```text
backend/
├── cmd/
│   └── roomusic/
│       └── main.go                 # process entry; no business policy
├── internal/
│   ├── app/                        # composition root, route mounting, lifecycle
│   ├── platform/
│   │   ├── config/                 # environment decoding and validation
│   │   ├── httpserver/             # server mechanics and common middleware
│   │   ├── observability/          # logger/correlation implementation
│   │   └── postgres/               # pool and transaction mechanics
│   ├── identity/
│   │   ├── domain/
│   │   ├── application/
│   │   ├── ports/
│   │   ├── adapters/postgres/
│   │   └── transport/httpapi/
│   ├── library/
│   │   ├── domain/
│   │   ├── application/
│   │   ├── ports/
│   │   ├── adapters/filesystem/
│   │   ├── adapters/postgres/
│   │   ├── parser/
│   │   └── transport/httpapi/
│   ├── catalog/                    # Release Graph and provenance
│   ├── search/                     # PostgreSQL-backed Core 0 search
│   └── operations/                 # Change Set and Operation Journal
├── migrations/
└── testdata/                       # small repository-wide fixtures only
```

The other capability directories follow the same internal shape only where each
layer is needed. Do not create empty folders or interfaces to make every module
look symmetrical.

## Ownership

- `identity` owns initialization closure, users, password verification, sessions,
  revocation, and admin/user authorization decisions.
- `library` owns allowed-root registration workflows, read-only source
  discovery, parsers, scan runs, diagnostics, and missing-source reconciliation.
- `catalog` owns ReleaseGroup, Release, Medium, Track, key field provenance, and
  conservative grouping rules.
- `search` owns query semantics and result projections while PostgreSQL remains
  the authority. It cannot write catalog truth through a search projection.
- `operations` owns Change Sets, Operation Journal events, idempotency records,
  revisions used by managed operations, and supported inverse actions.
- `platform` owns mechanisms shared by the process, never feature policy.
- `app` is the only composition root. It may import concrete adapters to wire
  dependencies; domain and application packages may not.

Artwork discovery belongs to the library scan boundary; catalog owns the
release-level association exposed to users. Managed thumbnail storage is a
filesystem adapter writing only to ROOMusic's data directory, never the music
root.

## Dependency Rules

Within a capability:

```text
transport -> application -> domain
                  |
                  v
                ports <- adapters
```

Go construction reverses the runtime dependency: `app` creates an adapter and
passes it through a consumer-owned port. Domain packages import only the standard
library and their own domain vocabulary where practical.

Across capabilities, call a published application contract or narrow port.
Never import another capability's `adapters`, `transport`, persistence row
types, or unexported policy package. Cross-capability orchestration belongs in a
clearly named application service, not a circular callback chain.

## File And Package Naming

- Use short, lowercase Go package names that describe a business concept:
  `session`, `scanrun`, `release`. Avoid `util`, `common`, `manager`, and
  `service` without a business qualifier.
- Name files by responsibility, such as `disable_root.go`,
  `reconcile_missing.go`, or `session_store.go`; do not mirror type names
  mechanically when one cohesive file is clearer.
- Keep tests beside their owner as `*_test.go`. Put feature fixtures in the
  nearest `testdata/`; keep large/private music libraries outside ordinary
  tests.
- REST routes use plural resources under `/api/v1`, for example
  `/api/v1/library-roots`, `/api/v1/scan-runs`, and `/api/v1/releases/{id}`.
- Export only contracts required by another package. Package-private is the
  default.

## Focused Example

A planned `DisableLibraryRoot` use case belongs in
`library/application/disable_root.go`. Its HTTP decoder belongs in
`library/transport/httpapi`; its compare-and-update SQL belongs in
`library/adapters/postgres`; its Change Set collaboration uses a narrow
published operations contract. None of these belong in a global controllers or
repositories directory.

## Anti-Patterns

- Top-level `controllers/`, `models/`, and `repositories/` buckets that mix
  every capability.
- A `platform/database` package exporting feature queries.
- A global service container looked up from handlers or domain functions.
- Parsing FLAC, applying Release grouping, opening transactions, and formatting
  REST responses in one scanner package.
- Creating `plugins/`, RPC hosts, or WASM loaders during Core 0 merely to reserve
  a future directory.

The capability map follows [Modular Design](../guides/modular-design.md) and the
current [Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
