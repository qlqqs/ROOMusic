# Error Handling

## Status And Goals

The Go error implementation has not been selected yet. Core 0 requires stable
error semantics across domain, application, adapters, REST, logs, and frontend.
Use standard Go wrapping and `errors.Is`/`errors.As` semantics unless a later
documented choice provides equivalent behavior.

Errors must preserve enough cause for operators while exposing only safe,
actionable information to users.

## Application Error Taxonomy

Use a narrow classified error at the application boundary. Feature-specific
codes may refine these classes without changing their HTTP meaning.

| Class / example code | HTTP | Meaning |
| --- | ---: | --- |
| `bad_request` | 400 | Malformed JSON, unsupported encoding, or invalid request shape |
| `validation_failed` | 422 | Decoded input violates a domain constraint |
| `unauthenticated` | 401 | No valid active session |
| `permission_denied` | 403 | Authenticated actor lacks authority |
| `not_found` | 404 | Authorized resource is absent |
| `revision_conflict`, `idempotency_conflict`, `setup_closed` | 409 | Current state conflicts with the command |
| `precondition_failed` | 412 | Explicit request precondition no longer holds |
| `unavailable` | 503 | Required Core 0 dependency is temporarily unavailable |
| `internal` | 500 | Unexpected implementation or infrastructure failure |

Path traversal, an out-of-root resolved target, and forbidden symlink behavior
are safe validation failures with stable codes, but responses must not disclose
the allowed host paths. Unsupported audio files encountered during scanning are
persisted scan diagnostics, not reasons to fail the entire REST request.

## REST Error Contract

Planned versioned REST shape:

```json
{
  "error": {
    "code": "revision_conflict",
    "message": "The library root changed. Refresh and try again.",
    "request_id": "req_...",
    "details": {
      "current_revision": 8
    }
  }
}
```

- `code` is stable and machine-readable.
- `message` is safe for display and must not contain SQL, stack traces, secrets,
  token material, or absolute server paths.
- `request_id` correlates the response to structured logs.
- `details` is optional, code-specific, bounded, and safe for the caller's role.
- The frontend branches on `code`, never on English message text.
- Unknown and unclassified errors map to `internal`; do not leak their cause.

One transport-level mapper owns application-class-to-status conversion and the
wire envelope. Individual handlers may add feature-specific safe details but do
not reinvent the envelope.

## Propagation Pattern

1. Adapters translate driver/provider failures into typed causes while preserving
   the wrapped original for internal diagnosis.
2. Application services add operation context and classify the failure at the
   owning boundary.
3. Transport maps the class once and writes the response.
4. Request middleware logs the final failure once with correlation fields.

Expected business failures such as stale revision, disabled root, missing
resource, or invalid credentials are not automatically error-level logs. An
unexpected PostgreSQL failure is logged at the request/job boundary, not at
every return site.

Planned Go shape:

```go
root, err := roots.Find(ctx, id)
if err != nil {
    return Result{}, fmt.Errorf("load library root: %w", err)
}
if root.Revision != expected {
    return Result{}, NewConflict("revision_conflict")
}
```

The exact constructor is selected with the backend scaffold; the important
contract is classification plus wrapped cause, not a specific error package.

## Async And Scan Failures

A scan run has explicit pending/running/succeeded/failed/cancelled/incomplete
states. Per-file parse failures and skipped formats are bounded diagnostics
linked to the scan run. They must not disappear into logs or silently become a
successful observation.

Only a complete successful run may perform negative reconciliation. Failure
handling must leave prior source availability unchanged and commit the run's
outcome/diagnostics safely.

Context cancellation propagates promptly. User cancellation is represented as a
scan state, not reported as an internal server failure. A disconnected HTTP
client does not justify abandoning a separately accepted durable operation
unless the operation contract explicitly says so.

## Security Behavior

Authentication messages remain non-enumerating where account existence is
sensitive. Authorization is checked before returning protected resource details.
Setup closure and session revocation are server-side state checks on every
relevant request.

Panic recovery at the outer process/request boundary emits a generic 500 and a
correlated internal log. Domain or adapter code must not use panic for ordinary
bad input or environmental failure.

## Anti-Patterns

- Returning `err.Error()` directly in JSON.
- Comparing error strings or frontend message text.
- Mapping every database constraint failure to 500.
- Logging the same error in repository, service, handler, and middleware.
- Catching an error, logging it, and returning success or an empty result.
- Treating incomplete scan output as a complete scan.
- Including absolute music paths in ordinary-user errors.

See [Logging Guidelines](./logging-guidelines.md), [Database Guidelines](./database-guidelines.md),
and the [Core 0 PRD](../../tasks/08-31-roomusic-core-0-rebuild/prd.md).
