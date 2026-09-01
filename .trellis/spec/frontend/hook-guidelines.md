# Hook Guidelines

## Role Of Hooks

Hooks own reusable stateful React coordination for one feature concern. They may
connect a typed feature API client to components, synchronize URL/local state,
or manage a lifecycle such as scan polling. They do not become service locators
or alternative business-policy engines.

The data-fetching/cache library has not been selected. The initial frontend task
must choose one mechanism and document it; do not mix ad hoc `useEffect` fetches
with a cache library across features.

## Planned Layers

```text
component -> feature hook -> feature API function -> shared HTTP transport
                                   |
                                   -> feature runtime decoder
```

- The shared transport owns credentials, common headers, safe body handling, and
  common error-envelope decoding.
- The feature API function owns endpoint, request/response DTO, and runtime
  decoding.
- The hook owns cache/query lifecycle and maps DTOs into feature state.
- The component owns rendering and local interaction.

## Hook Design

- Name hooks `use...` and make the name describe one concern:
  `useReleaseDetail`, `useScanRun`, `useDisableLibraryRoot`.
- Return a narrow typed object or discriminated state. Do not expose the entire
  cache client or a generic dispatch function.
- Keep derived values computed from their source instead of synchronized through
  another effect/state pair.
- Keep dependencies complete and stable. Fix unstable object/function creation
  at the owner; do not suppress hook dependency rules.
- Clean up timers, subscriptions, observers, and abort signals.
- Do not start a mutation during render.
- Avoid a hook that merely renames `useState`; extraction should centralize a
  real lifecycle or contract.

## REST And Session Behavior

All Core 0 requests target versioned REST. Production is same-origin, and the
HTTP client includes cookie credentials according to the selected transport
mechanism. No hook reads an auth cookie, stores JWT/access tokens, or attaches a
browser-readable bearer token.

A current-session hook consumes a safe `/api/v1` session/user response. Its role
projection controls presentation only; 401/403/409 behavior still comes from
the backend and must be handled for every query/mutation.

Runtime decoding happens once in the feature API boundary. Hooks receive typed
DTOs and classified `ApiError` values; they do not use `as` casts to recover
unknown response fields.

## Queries, Mutations, And Polling

- Query identity contains every server-visible input: resource ID, search text,
  filters, pagination, and viewer-specific scope where applicable.
- Search/filter state that should survive reload or be shareable belongs in the
  URL, then feeds the query key.
- Mutations send expected revision and idempotency fields when the REST contract
  requires them. On 409, preserve user context and expose refresh/reconcile
  behavior.
- Invalidate or update only the affected feature queries after success. Do not
  clear the entire cache because ownership is unclear.
- Prefer pessimistic updates for destructive-looking administrative state
  changes. Optimistic updates require a deterministic rollback and revision-safe
  response.
- Poll scan status only while it is non-terminal, back off according to the
  selected mechanism, pause when appropriate, and stop/clean up on terminal
  state or unmount.
- Abort obsolete searches and ignore stale completions through the chosen query
  mechanism; do not let a slower previous query replace a newer result.

Planned consumption shape:

```ts
const scan = useScanRun(scanRunId);

switch (scan.status) {
  case "loading":
  case "running":
  case "succeeded":
  case "failed":
  case "cancelled":
  case "incomplete":
    return renderScanState(scan);
}
```

The exact API depends on the selected library; exhaustive visible states do not.

## Tests

Test hooks through observable behavior with a controlled API boundary. Cover
success, classified error, cancellation/stale response, mutation conflict, and
polling termination when relevant. Do not assert internal hook call ordering as
a substitute for user-visible outcomes.

## Anti-Patterns

- Fetching directly in every component.
- One `useApi` hook with arbitrary paths and untyped generic return casts.
- A global `useAppState` hook containing auth, search, scan, and dialog state.
- Polling that continues after success or when the route is gone.
- Mutating cache data in place.
- Treating a frontend admin flag as permission to skip handling 403.
- Adding GraphQL/subscription hooks for Core 0 scan progress.

See [State Management](./state-management.md),
[Type Safety](./type-safety.md), and
[Cross-Layer Thinking](../guides/cross-layer-thinking-guide.md).
