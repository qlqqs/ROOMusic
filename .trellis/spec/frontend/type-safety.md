# Type Safety

## Baseline

The frontend uses TypeScript with strict checking. The initial scaffold must
enable `strict` and should enable checks that prevent unchecked indexed and
optional-property access unless a documented tool constraint blocks them. Do not
weaken compiler settings to integrate one feature.

External data enters as `unknown` and becomes trusted only through one runtime
decoder at the owning boundary. 旧手写 decoder 的实现随 `frontend/src/` 删除；恢复时
仍须按 feature API 边界解码，不得把旧 `api.ts` 写成当前存在的模块。运行时校验库尚未
采纳；禁止以 raw cast 绕过角色、枚举或必填字段检查。

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

每个 feature 为每种响应合同维护唯一 decoder。以下只演示当前扫描响应的部分字段，
不是完整 DTO；时间字段沿用 wire 名称与非空合同：

```ts
const scanStatuses = [
  "running",
  "succeeded",
  "failed",
  "canceled",
  "incomplete",
] as const;

type ScanStatus = (typeof scanStatuses)[number];

type ScanRunDTO = {
  id: string;
  status: ScanStatus;
  started_at: string;
};

function decodeScanRun(input: unknown): ScanRunDTO {
  // decoder 在此校验对象、标识、状态及时间；完整实现还需校验其余响应字段。
  // 消费者不重复解码。
  return scanRunDecoder.parse(input);
}
```

`scanRunDecoder` 仅为解码模式示意，不是已存在或已选定的库。完整扫描响应以
[运行合同](../backend/core0-runtime-contracts.md)和
[`library.Scan`](../../../backend/internal/library/scans.go)为准，当前没有已扫描文件数或百分比进度。
不得从示例增加后端未返回的字段；只在边界解码一次 `unknown`，其他消费者只接收类型化结果。

Decode the common error envelope even for non-2xx responses. Unknown response
codes remain representable as a safe `ApiError` fallback so an older frontend
does not crash on a newer server response.

## Domain States

Use discriminated unions and exhaustive switches for lifecycle and view states.
When the backend adds an enum value, TypeScript should identify every mapping
that needs a decision.

Distinguish:

- absent, null, empty, and unavailable;
- failed, canceled, and incomplete scans;
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

发行边界的有界合同必须在分配或解码全部嵌套项之前检查：列表每页最多 100 条，详情
最多 256 个 Medium、整张 Release 合计最多 10,000 条 Track，credits/evidence 和
diagnostics 使用后端同名上限。Track 上限是 Release 总量，不是“每个 Medium 各
10,000 条”；decoder 在映射下一组 Track 前先累计长度并 fail closed。管理员 evidence
中的 source ref 必须是有界安全相对路径，绝对路径和 `file://` 一律拒绝。

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
[Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
