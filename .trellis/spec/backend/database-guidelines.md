# Database Guidelines

## Core 0 Database Contract

PostgreSQL 18 is the only required business authority for Core 0. The current
[Compose file](../../../compose.yaml) provides it locally. Redis and Meilisearch
may be running for development, but application startup, sessions, scanning,
search, and the user-visible Core 0 loop must work without them.

No ORM, query generator, migration tool, or PostgreSQL driver has been selected
in this repository. Record those choices when the backend is initialized; do not
silently copy the V0 stack.

## Ownership And Access

Each capability owns its tables, queries, row mappings, and migrations. Another
capability accesses that data through a published application contract or
consumer-owned port, never by importing SQL or querying private tables.

Planned table families should make ownership visible, for example identity
users/sessions, library roots/scan runs/source observations, catalog release
graph entities, and operations change sets/journal events. Exact names are fixed
by the first reviewed migration, not by this documentation example.

Repository methods return domain or application values, not database row structs.
SQL nullability, driver types, and JSON representation stop at the adapter.

## Query Rules

- Use explicit columns. Avoid `SELECT *` in application queries and scans.
- Bind every value as a parameter. Dynamic sort/filter choices come from a
  server-owned allowlist, never string concatenation.
- Pass `context.Context` through queries and honor cancellation.
- Bound list and search queries with stable ordering and explicit pagination.
- Avoid N+1 access for Release -> Medium -> Track views; use a bounded query plan
  or batch by IDs, then assemble the typed projection at one owner.
- PostgreSQL basic search is the Core 0 implementation. A future Meilisearch
  adapter is a rebuildable projection and cannot become the write authority.
- Keep query performance observable and add indexes from measured access
  patterns. Do not index every candidate field preemptively.

## Transactions, Revision, And Idempotency

Application use cases define transaction boundaries; individual repositories do
not silently begin and commit independent transactions inside one business
operation.

Directory add, disable, and restore must atomically persist:

1. the resource state and incremented revision;
2. the Change Set state;
3. the Operation Journal event; and
4. the idempotency result or conflict evidence.

Use compare-and-update semantics such as:

```sql
UPDATE library_roots
SET status = $1, revision = revision + 1, updated_at = $2
WHERE id = $3 AND revision = $4;
```

Zero affected rows maps to a classified revision conflict, not an unconditional
retry. An idempotency key is scoped to its operation/actor context and tied to a
canonical request fingerprint. Repeating the same request returns the recorded
result; reusing the key for different input returns a conflict.

Do not hold a database transaction open while walking a library tree or reading
audio tags. Persist scan progress in bounded batches and finalize the scan with
a transaction that applies negative reconciliation only when the outcome is
complete and successful.

## Domain Integrity

- Encode required uniqueness, foreign keys, and lifecycle constraints in
  PostgreSQL as well as domain code where the database can enforce them.
- Use `timestamptz` and UTC instants for persisted time. Presentation timezone
  belongs at the client boundary.
- Store session token hashes only; never store or log the bearer token. Session
  rows include owner, expiry, revocation, and timestamps needed for immediate
  invalidation.
- Keep Track identity stable for the same root and normalized relative source
  path. Rename/move appears as an old missing source and a new source in Core 0;
  weak similarity cannot update the old identity.
- Preserve missing sources and derived entities for diagnosis. Core 0 performs
  no automatic physical purge.
- Store provenance for key metadata: current value, source kind, inferred flag,
  scan run, and observation time.
- Store artwork bytes in ROOMusic-managed data storage, not PostgreSQL; persist
  source/hash/MIME/dimensions/storage references.
- Use JSONB only for genuinely variable, versioned evidence or diagnostics.
  Do not hide core identities, relations, revisions, or query-critical fields in
  an untyped JSON document.

## Migrations

- Put ordered migration files in `backend/migrations/`.
- Every schema change is an explicit, reviewable migration; application startup
  must not infer schema from structs.
- Never edit a migration after it has been shared or applied outside a disposable
  local database. Add a corrective migration.
- Make additions backward-compatible across the intended deployment transition
  when feasible. Split destructive changes into expand, backfill, switch, and
  contract steps.
- A migration claiming to be reversible must actually restore the prior schema
  and data contract. Otherwise document restore/forward-fix requirements instead
  of writing a destructive fake down migration.
- Select and document the migration command/tool with the first migration. V0's
  historical tool choice is not automatically inherited.

## Testing

Repository integration tests use a disposable PostgreSQL database at the
supported version and cover constraints, rollback, conflict, idempotency, and
representative query shape. Scanner integration tests must prove incomplete
scans cannot mark sources missing.

## Anti-Patterns

- Cross-module joins from arbitrary repositories because all tables share one
  database.
- Long filesystem or network work inside a transaction.
- Last-write-wins updates without an expected revision.
- Redis-backed sessions or Meilisearch-only search in the Core 0 acceptance path.
- Returning raw SQL errors or constraint names to REST clients.
- Treating Operation Journal, scan history, and runtime logs as one table.

These rules implement the transaction and authority requirements in the
[Core 0 PRD](../../tasks/08-31-roomusic-core-0-rebuild/prd.md).
