# Type Safety

## Baseline

The frontend uses TypeScript with strict checking. The initial scaffold must
enable `strict` and should enable checks that prevent unchecked indexed and
optional-property access unless a documented tool constraint blocks them. Do not
weaken compiler settings to integrate one feature.

External data enters as `unknown` and becomes trusted only through one runtime
decoder at the owning boundary. No runtime validation library has been selected;
choose and document one when the REST client is implemented rather than adding
several feature-local solutions.

## Type Ownership

- REST request/response DTOs and their decoders live in the owning feature's
  `api/` folder.
- The common REST error envelope and HTTP result mechanics live in
  `shared/api`.
- Domain/view models live with the feature that interprets them.
- Component prop and local-state types stay beside the component unless they are
  a published feature contract.
- Do not create a global `types/` directory or duplicate backend storage types.

Wire DTOs describe the published REST contract, not PostgreSQL rows. Map them at
the API/feature boundary when the UI needs parsed dates, grouped media, labels,
or other display-specific structure. Keep identifiers opaque strings; never
derive identity from title, track number, array index, or file path.

## Runtime Decoding

A feature owns one decoder per response contract:

```ts
const scanStatuses = [
  "pending",
  "running",
  "succeeded",
  "failed",
  "cancelled",
  "incomplete",
] as const;

type ScanStatus = (typeof scanStatuses)[number];

type ScanRunDTO = {
  id: string;
  status: ScanStatus;
  observedFiles: number;
  startedAt: string | null;
};

function decodeScanRun(input: unknown): ScanRunDTO {
  // The selected decoder validates object shape, bounded numbers, status, and
  // nullable timestamps here. Consumers never repeat those checks.
  return scanRunDecoder.parse(input);
}
```

`scanRunDecoder` is illustrative; its library/API is not selected. The stable
rule is that `unknown` is decoded once and all consumers receive `ScanRunDTO`.

Decode the common error envelope even for non-2xx responses. Unknown response
codes remain representable as a safe `ApiError` fallback so an older frontend
does not crash on a newer server response.

## Domain States

Use discriminated unions and exhaustive switches for lifecycle and view states.
When the backend adds an enum value, TypeScript should identify every mapping
that needs a decision.

Distinguish:

- absent, null, empty, and unavailable;
- failed, cancelled, and incomplete scans;
- loading initial data from refreshing stale data;
- permission denial from authentication expiry;
- revision conflict from generic validation failure;
- source evidence from a display fallback.

Do not make fields optional merely to silence initialization errors. Optionality
must match the wire/domain contract.

## Dates, Numbers, And Paths

- REST timestamps are explicit ISO/RFC 3339 strings; parse/format at one UI
  boundary and preserve the original instant.
- Do not assume JavaScript numbers can safely represent arbitrary database
  integers. Pagination/count contracts must define safe bounds or use strings
  where necessary.
- Paths are server-side sensitive values. Ordinary DTOs use root IDs and safe
  labels/relative display values, never a value that frontend code joins into a
  filesystem path.
- Resource and revision values come from the server response and are carried
  unchanged into mutations.

## Forbidden Patterns

- `any`, `// @ts-ignore`, unchecked double assertions, or broad
  `Record<string, unknown>` passed into components.
- `response.json() as ReleaseDetail`.
- Non-null assertions where loading/absence is a valid state.
- Comparing REST error message strings.
- Duplicating string unions in several components.
- Using GraphQL-generated types in Core 0 or importing types from V0 source.
- A generic API function whose caller supplies an arbitrary type parameter and
  receives an unvalidated cast.

Narrow assertions such as `as const` are acceptable when they make a literal
contract more precise. Any unavoidable unsafe assertion must be isolated at a
tested boundary and documented.

## Contract Changes

When changing a REST field or enum, search backend presenter, API documentation,
decoder, DTO, feature mapping, components, tests, and cached/persisted state.
Follow the [Cross-Layer Thinking Guide](../guides/cross-layer-thinking-guide.md).
The current REST and security scope comes from the
[Core 0 PRD](../../tasks/08-31-roomusic-core-0-rebuild/prd.md).
