# Cross-Layer Thinking Guide

## Purpose

Cross-layer changes fail when data shape, authority, identity, or error meaning
changes in one place but not another. Map the full flow before implementation
and give every transformation one owner.

## Core Flows

Directory management:

```text
Admin form
  -> typed REST request
  -> cookie session + CSRF/origin + role checks
  -> library-root use case
  -> canonical path containment adapter
  -> revision/idempotency transaction
  -> Change Set + Operation Journal
  -> safe REST DTO
  -> cache/view update
```

Scanning and browsing:

```text
Read-only filesystem -> parser observations -> scan policy -> PostgreSQL
  -> Release Graph query -> REST DTO -> frontend decoder -> feature view model
```

Future Music Steward operation:

```text
User / scheduled intent
  -> Assistant approval, Steward independent review, or Operator no-approval path
  -> typed Change Set
  -> backend tool authority and current-principal recheck
  -> revision/idempotency/physical-safety validation
  -> transactional or staged executor
  -> Operation Journal + safe REST DTO
  -> operation/recovery UI
```

For each arrow, record the input type, output type, possible error, correlation
identifier, and owner. A diagram is useful when a flow crosses three or more
boundaries or changes persistent state.

## Validation Ownership

"Validate once" means one owner for each interpretation, not trusting an
earlier, weaker layer:

- Frontend validation provides prompt feedback only.
- REST transport decodes `unknown` input, limits shape/size, and creates typed
  command values.
- Application/domain code checks role, lifecycle, revision, and domain rules.
- Filesystem and database adapters enforce containment, uniqueness, and physical
  constraints at the final authority boundary.
- Frontend decodes each response shape once in the API boundary. Components do
  not cast raw JSON.

Security checks are deliberately repeated at different authority boundaries;
payload parsing and business policy are not duplicated among peer consumers.

## Contract Checklist

Before a cross-layer change:

- [ ] Identify the capability that owns the behavior and data.
- [ ] Define request, response, and classified error contracts.
- [ ] Define stable identity, revision, idempotency, and correlation fields.
- [ ] Decide transaction and partial-failure behavior.
- [ ] Decide what ordinary users may see; exclude secrets and raw server paths.
- [ ] Decide loading, empty, stale, conflict, canceled, and failure UI states.
- [ ] Confirm Redis, Meilisearch, Agent, playback, and plugins are not accidental
      Core 0 dependencies.
- [ ] For a future Agent operation, keep approval state, execution state, and
      recovery state distinct; identify the backend tool that owns each side
      effect.

After implementation:

- [ ] Test round-trip serialization and malformed input.
- [ ] Test the backend authorization and physical safety boundary.
- [ ] Test conflict/rollback behavior for persistent changes.
- [ ] Verify one decoder/type owner per wire payload.
- [ ] Verify logs, scan history, and Operation Journal remain distinct.
- [ ] Verify Operator skips approval only, Steward cannot self-review, and no
      Agent payload can grant itself a capability.
- [ ] Search every consumer when a field, enum, endpoint, or error code changes.

## Examples

If `LibraryRootDTO.status` gains `disabled`, update the backend projection,
documented REST contract, frontend decoder, discriminated union, status render,
mutation invalidation, and tests. Do not add `as LibraryRoot` casts to individual
components.

If a scan fails after reading part of a root, persist the failed/incomplete scan
status and diagnostics but do not run missing-source reconciliation. That rule
must be enforced by the scanner application policy and transaction, not inferred
by the UI from a progress percentage.

For cover delivery, REST returns an authorized resource ID and MIME/cache
metadata. The browser never receives an absolute library path, and the database
stores a managed asset reference rather than a large image binary.

## Anti-Patterns

- A React component knows PostgreSQL column names or absolute file paths.
- An HTTP handler performs SQL and decides scan reconciliation inline.
- Every feature has a slightly different REST error parser.
- A successful HTTP response is treated as proof that an operation committed,
  while the operation status contract says it is still pending.
- A new search field is written to Meilisearch only, making an optional future
  projection the source of truth.

The current cross-layer acceptance paths are defined in the
[Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md); phase boundaries
are summarized in [Product Goals](./product-goals.md).
