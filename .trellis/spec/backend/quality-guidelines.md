# Backend Quality Guidelines

## Status

There is no Go module or test suite yet. These are required gates for the Core 0
scaffold and subsequent work. The first backend setup change must expose
repeatable repository commands and update this file with their exact names.

## Design Requirements

- Keep capability policy cohesive and behind narrow, consumer-owned interfaces.
- Keep dependency direction inward; wire concrete adapters only in the
  composition root.
- Keep functions at one abstraction level with explicit I/O and side effects.
- Pass context to I/O boundaries and make clock/ID generation injectable where
  deterministic tests need control.
- Decode external input once, then pass typed values. Re-check authorization and
  physical constraints at their authoritative backend boundaries.
- Wrap and classify errors; never discard them or expose internal causes.
- Make persistent changes transactional, revision-aware, and idempotent when the
  API promises retry safety.
- Preserve read-only source libraries and PostgreSQL-only Core 0 operation.

Small functions are judged by cohesion and cognitive load, not a line limit. A
single readable pure transformation may be longer than a handler that mixes
decoding, authorization, SQL, and presentation and therefore must be split.

## Forbidden Patterns

- Cross-module access to private tables, adapters, row structs, or helpers.
- Import cycles, a global service locator, or package-level mutable business
  state.
- A broad `Repository`/`Service` interface spanning unrelated capabilities.
- HTTP handlers containing SQL or filesystem traversal.
- Business logic in migrations, middleware, database triggers, or React clients
  without a matching backend owner.
- `panic` for user input or ordinary infrastructure errors; `log.Fatal` outside
  the process entrypoint.
- Unbounded goroutines, channels, result sets, request bodies, diagnostics, or
  directory work queues.
- Starting background work without cancellation, ownership, concurrency limits,
  and observable completion.
- Silent fallback from PostgreSQL to Redis/Meilisearch or from REST to GraphQL.
- Default source-file writes, tag updates, moves, deletes, or directory symlink
  traversal.
- Copying V0 code without revalidating assumptions and tests against Core 0.
- Letting an Agent, model adapter, Review Subagent, browser, or plugin execute
  SQL, shell, or filesystem mutations outside a registered backend tool.
- Treating Operator mode as a reason to skip authorization, argument validation,
  revision/idempotency checks, journaling, or recovery classification.
- Treating the main Agent's own output as independent Steward approval.

## Test Ownership

| Behavior | Minimum evidence |
| --- | --- |
| Pure domain/parser rule | Table-driven unit tests including malformed and boundary inputs |
| PostgreSQL adapter | Integration test with PostgreSQL 18, constraints, rollback, and query result mapping |
| REST endpoint | Request/response, auth, safe error, content type, and correlation assertions |
| Session lifecycle | Setup closure, login/logout, expiry, revocation, disabled user, token-hash storage, cookie flags |
| Path boundary | traversal, relative/absolute normalization, symlink escape/broken target, and allowed-root containment |
| Scanner | initial formats, CUE, cancellation, partial failure, complete-only missing reconciliation, repeat identity |
| Directory mutation | transaction, expected revision, idempotent retry, key/input conflict, Change Set, and inverse action |
| Future Agent tool | mode/approval matrix, independent review binding, backend recheck, tool scope, idempotency, journal, and recovery/irreversible marker |
| Concurrency-sensitive code | deterministic contention test and race detector where practical |

Tests assert behavior, not implementation call counts. Mocks are appropriate for
narrow ports in application tests; they do not replace PostgreSQL or filesystem
integration tests for physical guarantees. Fixtures must be small, legal to
store, and contain no private user library data.

## Planned Gates

Once the Go module exists, every backend change runs the applicable subset of:

```bash
gofmt
go test ./...
go vet ./...
go build ./...
```

Concurrency-heavy changes also run `go test -race ./...` in a supported
environment. Database changes run migrations against a disposable PostgreSQL 18
instance and repository integration tests. Deployment changes validate
`docker compose config`.

Do not invent a linter or coverage threshold here. If static analysis, migration,
or vulnerability tools are selected, pin/configure them in the repository,
expose one repeatable command, and update this guide. A tool gate is useful only
when local and CI behavior agree.

## Review Checklist

- [ ] The change is the smallest complete behavior at the owning capability.
- [ ] No unrelated refactor or speculative adapter is bundled.
- [ ] Public interfaces and exported names are narrower than their implementations.
- [ ] Security decisions remain backend-authoritative.
- [ ] Transactions, conflict, cancellation, and partial failure are explicit.
- [ ] Errors and logs are classified, correlated, and redacted.
- [ ] Representative success and dangerous failure paths are tested.
- [ ] The production Core 0 loop still works with Redis and Meilisearch absent.
- [ ] No music-source mutation or raw path disclosure was introduced.
- [ ] Any future Agent or Operator path uses the shared authority/executor and
      cannot bypass it through a model adapter, plugin, CLI, or internal route.
- [ ] Documentation/specs changed when a contract or selected tool changed.

## Anti-Example

A patch adding `POST /scan` that starts an unbounded goroutine, accepts an
arbitrary path, logs that path, and returns 200 has few lines but is not a small
change. The complete change belongs to the library module and includes admin
authorization, allowed-root identity, bounded scheduling, durable scan state,
safe errors/logs, and tests.

See [Engineering Principles](../guides/engineering-principles.md),
[Modular Design](../guides/modular-design.md), and the
[Core 0 PRD](../../tasks/08-31-roomusic-core-0-rebuild/prd.md).
