# Modular Design

## Architecture Style

Core 0 is a modular monolith. First-party capabilities compile into one Go
binary, but they keep explicit ownership and typed contracts. Deployment unity
does not permit package-level entanglement.

The default dependency direction is:

```text
HTTP transport -> application use case -> domain + consumed ports
                                             ^
                                             |
                         PostgreSQL/filesystem adapters
```

The composition root constructs adapters and injects them into application
services. Domain and application code must not discover implementations through
a global registry or service locator.

## Capability Ownership

The initial ownership map is a planned Core 0 contract:

| Capability | Owns | Does not own |
| --- | --- | --- |
| Identity and access | setup closure, users, password verification, sessions, role decisions | frontend navigation or library policy |
| Library and scanner | allowed-root registration use cases, filesystem observations, scan runs, parser coordination, missing reconciliation | Release merge policy or arbitrary file management |
| Release Graph | ReleaseGroup/Release/Medium/Track rules and provenance links | filesystem walking or HTTP decoding |
| Search | basic PostgreSQL-backed query contract and result projection | authoritative catalog writes |
| Operations | Change Set, Operation Journal, idempotency, revision and supported recovery workflows | ordinary runtime logs or Agent reasoning |
| Platform | process configuration, HTTP server, database connection, observability plumbing, clock/ID implementations | business decisions |

A capability owns its policy, data-writing use cases, ports, and tests. Other
capabilities consume its published application contract. They do not query its
tables, import private helpers, or reproduce its rules.

## High Cohesion

A unit is cohesive when its name explains one business responsibility and its
reasons to change come from that responsibility. Use these tests:

- A function should operate at one abstraction level. Split a function that
  simultaneously decodes HTTP, checks authorization, opens a transaction,
  walks files, and formats a response.
- Keep deterministic parsing and transformation pure where practical. Make
  database, filesystem, clock, network, and process effects explicit ports.
- Keep a rule beside its vocabulary and tests. For example, complete-scan-only
  missing reconciliation belongs to scanner/catalog application policy, not a
  generic database helper.
- Small means low cognitive load, not an arbitrary line count. Do not replace a
  readable flow with many one-line forwarding functions.

## Low Coupling

- Interfaces are defined by the consumer boundary and expose only what the use
  case needs. A scanner needing `RecordObservation` must not depend on a
  repository with unrelated user, search, and operation methods.
- Cross-module values use stable typed contracts. Do not pass database rows,
  HTTP request types, React view models, or unvalidated maps across modules.
- Adapters implement ports; ports do not import adapters. Provider-specific
  errors and response shapes are normalized before entering application logic.
- Cross-module cycles are forbidden. Resolve a cycle by moving orchestration to
  an application boundary or extracting a smaller consumer-owned contract, not
  by adding callbacks to both sides.
- Shared packages contain stable, policy-free mechanics only. `shared/utils` is
  not a home for unowned domain behavior.

## Boundary Validation

Decode untrusted input once at its entry boundary, normalize it to a typed value,
then pass typed contracts inward. Validation still has distinct owners:

- Transport validates shape, size, encoding, and required fields.
- Application/domain validates business invariants and current authority.
- Adapters enforce physical safety and storage constraints, such as resolved
  path containment and database uniqueness.

Frontend validation improves usability but never satisfies a backend security
check. A valid-looking root path still requires server-side canonicalization,
symlink handling, containment checks, role checks, and revision checks.

## Example

```go
// Planned shape: the library application boundary consumes a narrow port.
type RootStore interface {
    Find(ctx context.Context, id RootID) (LibraryRoot, error)
    UpdateStatus(ctx context.Context, id RootID, expected Revision, next Status) error
}

type DisableRoot struct {
    roots RootStore
    ops   ChangeRecorder
}
```

`DisableRoot` owns the orchestration. An HTTP handler decodes a request and calls
it; a PostgreSQL adapter implements `RootStore`; Operations implements the
published `ChangeRecorder` contract. The handler does not issue SQL, and the
repository does not decide whether the caller is an administrator.

## Anti-Patterns

- A `services` package importing every module and exposing arbitrary methods.
- A `Repository` interface containing all tables because it is convenient to
  mock once.
- Scanner code writing Release Graph tables directly and bypassing catalog
  invariants.
- Search results becoming the authoritative write model.
- Capability Registry becoming a service locator used by Core 0 modules.
- Designing dynamic plugin loading, RPC, or WASM before an implemented feature
  needs an out-of-process boundary.

The visual intent is summarized by the
[architecture canvas](../../../docs/architecture/roomusic-modular-plugin-architecture.canvas.tsx),
while the [Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md) remains
authoritative for behavior.
