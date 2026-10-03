# Type Safety

## Baseline

The frontend uses TypeScript with strict checking. `tsconfig.json` 已开启 `strict`、`noUncheckedIndexedAccess`、
`exactOptionalPropertyTypes` 与 `noFallthroughCasesInSwitch`，类型检查范围包括 `src`、`e2e` 与各配置文件。Do not
weaken compiler settings to integrate one feature；某个依赖与选项不兼容时，只关闭被阻断的单个选项并记录原因。

External data enters as `unknown` and becomes trusted only through one runtime
decoder at the owning boundary. 解码用 `shared/api/decode.ts` 的手写原语（对象、字符串、布尔、安全整数、有限数字（曲目时长）、RFC 3339 时间、
UUID、枚举、数组、可选与可空），不引入运行时校验库；原语失败时抛出带字段路径的 `DecodeError`，只用于测试诊断，不展示给用户。
禁止以 raw cast 绕过角色、枚举或必填字段检查。

decoder 与后端的一致性由样本把关：后端集成测试 `TestPostgreSQLAPIResponseSamples` 经真实 handler 产生响应，写成
`backend/internal/app/testdata/api-samples/` 下的 JSON 样本（只含状态码与响应体）；`src/test/api-contract.test.ts` 遍历全部
样本，每个样本必须对应一个 decoder 并解码成功、状态码一致，且 decoder 往返后与样本相同（后端新增字段而 decoder 没有保留
它时失败）。后端合同变化时先更新样本（`ROOMUSIC_UPDATE_API_SAMPLES=1 ./scripts/test-integration.sh`），再同步 decoder。

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

每个 feature 为每种响应合同维护唯一 decoder，字段名沿用 wire 名称，可选字段与后端“缺失时省略”的合同一致。以下是目录操作记录
的 decoder（节选自 `features/library/api/decoders.ts`）：

```ts
import { decode as d } from "../../../shared/api";

export const rootOperationKinds = ["create", "disable", "restore"] as const;
export const rootOperationStatuses = ["succeeded", "failed"] as const;

// root_id、revision 与 error_code 在记录没有对应值时被省略。
const rootOperationDecoder = d.object({
  id: d.uuid(),
  operation: d.oneOf(rootOperationKinds),
  status: d.oneOf(rootOperationStatuses),
  created_at: d.timestamp(),
  root_id: d.optional(d.uuid()),
  revision: d.optional(d.integer({ min: 1 })),
  error_code: d.optional(d.string({ min: 1, max: 64 })),
});

// 目录操作记录最多 100 条。
const rootOperationListDecoder = d.object({ items: d.array(rootOperationDecoder, { max: 100 }) });

export const decodeRootOperationList = d.defineDecoder(rootOperationListDecoder);
export type RootOperationDTO = d.Infer<typeof rootOperationDecoder>;
```

列表上限沿用后端：目录操作记录、诊断明细与诊断类别各最多 100 条；后端没有上限的列表（目录、成员）不限条数，只限字段长度。
数组先检查长度再逐项映射。完整扫描响应以[运行合同](../backend/core0-runtime-contracts.md)和
[`library.Scan`](../../../backend/internal/library/scans.go)为准，当前没有已扫描文件数或百分比进度；
不得增加后端未返回的字段，只在边界解码一次 `unknown`，其他消费者只接收类型化结果。

请求层 `apiRequest(spec, decode)` 返回 `{ status, data }`（状态码有业务含义：开始扫描 202 新建 / 200 已有，目录新增 201），
失败时抛出携带 `ApiFailure` 的 `ApiError`，分为四类：`http`（后端的合法错误信封）、`invalid_response`（收到响应但不可信：
非 JSON、HTML 回落页、信封或 decoder 不合法，不进入缓存）、`network`（没有拿到响应，对写请求结果未知）、`aborted`
（调用方取消）。`isOutcomeUncertain(failure)` 把网络失败、无效响应与 5xx 归为“结果不确定”，4xx 是明确的拒绝；界面按
`error.code` 选文案，不比较后端 message。

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
中的 source ref 必须是有界安全相对路径，绝对路径和 `file://` 一律拒绝。M1b 的 `features/catalog/api/decoders.ts` 按上面实现：
Track 与曲目署名的合计在解码每个介质之前累计，source ref 拒绝以 `/` 开头、反斜杠、`\0`、首段含 `:` 与空段、`.`、`..`。

后端会增长的词表（署名角色、证据字段、来源、可信度、处理方式、原因码、归组方式、来源与介质类型）按**字符串**解码，不用枚举：后端新增取值不会让整页解码失败，
显示时由 `features/catalog/labels.ts` 给已知值中文名、未知值原样显示（样本里出现过的取值都有中文名，由测试保证）。

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
- 在 `shared/api/request.ts` 之外直接调用 `fetch`（`no-restricted-globals` 与 `no-restricted-properties` 会报错）。

Narrow assertions such as `as const` are acceptable when they make a literal
contract more precise. Any unavoidable unsafe assertion must be isolated at a
tested boundary and documented.

## Contract Changes

When changing a REST field or enum, search backend presenter, API documentation,
decoder, DTO, feature mapping, components, tests, and cached/persisted state.
Follow the [Cross-Layer Thinking Guide](../guides/cross-layer-thinking-guide.md).
The current REST and security scope comes from the
[Core 0 PRD](../../tasks/archive/2026-09/08-31-roomusic-core-0-rebuild/prd.md).
