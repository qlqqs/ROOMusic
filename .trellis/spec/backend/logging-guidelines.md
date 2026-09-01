# Logging Guidelines

## Contract

Core 0 emits structured JSON logs to stdout/stderr for container collection.
当前使用 Go 标准库 `log/slog` 的 JSON handler 输出结构化日志；事件 schema 和
脱敏规则仍独立于具体实现。

Logs explain runtime behavior. They do not replace scan-run history, source
observations, Change Sets, or Operation Journal records, all of which are
queryable business records with their own retention and recovery semantics.

## Event Shape

Every event includes:

- `time` as an RFC 3339 UTC timestamp;
- `level`;
- stable `event` name;
- `message` for human context;
- `module` using the owning capability name; and
- relevant correlation identifiers.

Example planned output:

```json
{
  "time": "2026-09-01T12:00:00Z",
  "level": "INFO",
  "event": "library.scan.completed",
  "message": "library scan completed",
  "module": "library",
  "scan_run_id": "scan_...",
  "library_root_id": "root_...",
  "observed_files": 1842,
  "duration_ms": 9314
}
```

Use stable dotted event names such as `http.request.completed`,
`identity.session.revoked`, `library.scan.started`, and
`operations.change_set.failed`. Do not put IDs or status values inside the
event name.

## Correlation

- Generate or validate a bounded `request_id` at the HTTP edge and return it in
  responses. Do not trust an unbounded caller-supplied value.
- Propagate correlation through context rather than rebuilding logger fields in
  every function.
- Add `scan_run_id`, `operation_id`, `change_set_id`, and `task_id` when
  those scopes exist.
- Future Music Steward flows add `agent_run_id`, `review_run_id`, `mode`,
  `tool`, `approval_status`, and `operation_id` when applicable. Use stable IDs
  and enum values; do not log prompts, chain-of-thought, raw model responses, or
  full Change Set payloads.
- Include stable actor/user identifiers only when necessary for security or
  operation diagnosis; do not include email, display name, or session token.
- A background operation accepted by a request retains the initiating
  `request_id` and gains its own durable operation identifier.

## Levels

| Level | Use |
| --- | --- |
| `DEBUG` | Local diagnostic details disabled by default; never a secret bypass |
| `INFO` | Process lifecycle and meaningful successful state transitions |
| `WARN` | Rejected/degraded but handled conditions requiring attention |
| `ERROR` | Unexpected failure or exhausted operation that needs intervention |

Do not log every 4xx as an error. Invalid credentials and stale revisions are
expected outcomes; aggregate or warn only when security/operational policy
requires it. A PostgreSQL outage or violated internal invariant is an error.

## HTTP, Scan, And Operation Events

A single HTTP middleware logs route template, method, status, duration, request
ID, and safe actor ID. Never log query strings or request bodies wholesale.

Scanner logging is aggregate-first. Log scan start/final state and meaningful
batch or failure summaries. Per-file parse and unsupported-format details belong
in bounded scan diagnostics; emitting one info log per track is unacceptable for
100,000-track libraries.

Log an operation's lifecycle with its durable `operation_id`, but keep
before/after state and recovery data in Change Set/Operation Journal storage.
Logging "rollback available" is not a recovery implementation.

Assistant, Steward, and Operator use the same operation event vocabulary. Logs
may state that approval was user-provided, reviewer-provided, or not required;
they must not describe Operator as "auto-approved" and must not treat a Review
Subagent response as proof that execution committed. The durable journal owns
approval references, execution status, and rollback state.

## Sensitive Data

Never log:

- passwords, password hashes, cookie values, session tokens, CSRF secrets;
- database URLs, Redis/Meilisearch keys, provider secrets, or environment dumps;
- raw authorization headers or full request/response bodies;
- unrestricted absolute NAS paths or filenames containing private user data;
- audio bytes, artwork bytes, tags wholesale, or stack traces sent to clients.

Prefer `library_root_id`, normalized relative path when operationally necessary,
and a bounded/sanitized error class. Administrative diagnostics may expose more
through an authorized API, but logs still follow least disclosure.

The sample values in [.env.example](../../../.env.example) demonstrate which
configuration categories are secrets; examples are not permission to log real
values.

## Failure Logging

Log unexpected errors once at the outer boundary with the classified error code,
wrapped internal cause, and correlation fields. Avoid duplicate stack traces and
avoid manually concatenated log strings. Logging must never change return values
or commit behavior.

Process startup validates configuration and logs only which capabilities are
enabled. It must not print connection strings. A panic recovery event is
structured and correlated; it does not expose panic detail to REST clients.

## Anti-Patterns

- Plain-text `fmt.Printf` diagnostics in request, scan, or repository code.
- Logging the complete library path to make debugging easier.
- Using logs as the only record of a directory disable/restore action.
- Dynamic message text as the only searchable event identifier.
- High-cardinality metrics disguised as one log per file.
- Claiming Redis or Meilisearch health is required for the Core 0 application.

These requirements come from the
[Core 0 observability contract](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md)
and the repository-local
[large-library product context](../guides/product-goals.md).
