import { decode as d } from "../../../shared/api";

// 自动整理的管理员接口（M7 A、B1、C；合同 backend curation-contracts.md 第 7、11、15 节）。
// 枚举用 oneOf：后端新增取值时样本契约测试先失败，在这里补上才算确认前端已经知道。

const MAX_PAGE_ITEMS = 100;
const paginationDecoder = d.object({
  page: d.integer({ min: 1 }),
  page_size: d.integer({ min: 1, max: MAX_PAGE_ITEMS }),
  total: d.integer({ min: 0 }),
});

const userRefDecoder = d.object({ id: d.uuid(), username: d.string({ max: 200 }) });

// 总开关：enabled 是开关本身，active 是“现在自动写入会不会被接受”；enabled_at 在从未打开过时为 null，关掉之后保留打开时的值。
const settingsDecoder = d.object({
  active: d.boolean(),
  enabled: d.boolean(),
  enabled_at: d.nullable(d.timestamp()),
  enabled_by: d.nullable(userRefDecoder),
  inactive_reason: d.nullable(d.oneOf(["disabled", "enabler_unavailable"] as const)),
  paused: d.boolean(),
});

export const SOURCE_CAPABILITIES = ["release_lookup", "recording_lookup", "artist_lookup", "cover_art", "lyrics", "fingerprint_lookup"] as const;
export type SourceCapability = (typeof SOURCE_CAPABILITIES)[number];

export const SOURCE_INACTIVE_REASONS = ["curation_disabled", "enabler_unavailable", "source_disabled", "fingerprint_unavailable", "key_missing", "key_invalid"] as const;
export type SourceInactiveReason = (typeof SOURCE_INACTIVE_REASONS)[number];

// 需要密钥的源（AcoustID：应用密钥与用户密钥）才有 keys，按声明顺序每把一项：只给出有没有、末四位与服务端用过之后的结论（unchecked 是还没用过）。
const sourceKeyDecoder = d.object({
  configured: d.boolean(),
  last_four: d.string({ max: 4 }),
  name: d.string({ min: 1, max: 32, pattern: /^[a-z_]+$/ }),
  status: d.oneOf(["unchecked", "valid", "invalid"] as const),
});

// 一个外部源：enabled 是它自己的开关，active 是“后台现在能不能用它”；不能用时 inactive_reason 依次是总开关、开启者、源自己的开关、
// 指纹工具、有密钥没填、有密钥无效。
const sourceDecoder = d.object({
  active: d.boolean(),
  capabilities: d.array(d.oneOf(SOURCE_CAPABILITIES), { min: 1, max: SOURCE_CAPABILITIES.length }),
  enabled: d.boolean(),
  id: d.string({ min: 1, max: 64, pattern: /^[a-z0-9._-]+$/ }),
  inactive_reason: d.nullable(d.oneOf(SOURCE_INACTIVE_REASONS)),
  keys: d.optional(d.array(sourceKeyDecoder, { min: 1, max: 8 })),
  name: d.string({ min: 1, max: 200 }),
});
const sourcesDecoder = d.object({ items: d.array(sourceDecoder, { max: 50 }) });

export const PROGRAM_STATES = ["running", "waiting_for_scan", "idle", "paused", "disabled", "unavailable"] as const;
export type ProgramState = (typeof PROGRAM_STATES)[number];

const count = () => d.integer({ min: 0 });
// 后台的进度：releases 只算可见发行；processed 是按当前规则版本处理过的（输入变了、还没重新处理的仍算在内）。
const progressDecoder = d.object({
  batch: d.nullable(d.object({ id: d.uuid(), opened_at: d.timestamp(), operations: count() })),
  last_pass_finished_at: d.nullable(d.timestamp()),
  pending: d.object({ waiting_model: count() }),
  releases: d.object({ matched: count(), no_match: count(), processed: count(), total: count(), try_later: count(), uncertain: count() }),
  rule_version: d.integer({ min: 1 }),
  state: d.oneOf(PROGRAM_STATES),
});

// 批次：一段连续的后台工作；closed_at 为空表示还在进行。改动数与已撤销数由后端按操作记录统计。
const batchDecoder = d.object({
  closed_at: d.nullable(d.timestamp()),
  id: d.uuid(),
  opened_at: d.timestamp(),
  operations: count(),
  reverted: count(),
  tier: d.oneOf(["program", "model"] as const),
});
const batchPageDecoder = d.object({ items: d.array(batchDecoder, { max: MAX_PAGE_ITEMS }), pagination: paginationDecoder });

// 范围撤销的一段：reverted 只算这次调用实际撤销的；skipped 的 code 是开放集合（撤销冲突、已撤销，也可能是临时性失败），
// 只约束形状，未知的码有兜底文案。next_cursor 为 null 表示做完。
const scopeRevertDecoder = d.object({
  next_cursor: d.nullable(d.string({ min: 1, max: 200 })),
  reverted: count(),
  skipped: d.array(d.object({ operation_id: d.uuid(), code: d.string({ min: 1, max: 64, pattern: /^[a-z0-9_]+$/ }) }), { max: MAX_PAGE_ITEMS }),
});

// 标记里展示用的值：按主题有不同的形状（字段的意图、外部封面歌词的来源与编号、外部对应的来源与编号、归组的目标专辑），
// 全部是不可信文本。这里只约束为有界的 JSON 对象并原样保留，展示时按已知的键取值。
export type SuppressionValue = { readonly [key: string]: JsonValue };
type JsonValue = string | number | boolean | null | readonly JsonValue[] | { readonly [key: string]: JsonValue };
const MAX_VALUE_DEPTH = 4;
const MAX_VALUE_TEXT = 4096;
function jsonValue(input: unknown, path: string, depth: number): JsonValue {
  if (input === null || typeof input === "boolean") return input;
  if (typeof input === "number") return d.number()(input, path);
  if (typeof input === "string") return d.string({ max: MAX_VALUE_TEXT })(input, path);
  if (depth >= MAX_VALUE_DEPTH) throw new d.DecodeError(path, "嵌套太深");
  if (Array.isArray(input)) {
    if (input.length > 100) throw new d.DecodeError(path, "列表太长");
    return input.map((item, index) => jsonValue(item, `${path}[${index}]`, depth + 1));
  }
  if (typeof input === "object") {
    const entries = Object.entries(input);
    if (entries.length > 50) throw new d.DecodeError(path, "键太多");
    return Object.fromEntries(entries.map(([key, value]) => [key, jsonValue(value, `${path}.${key}`, depth + 1)]));
  }
  throw new d.DecodeError(path, "不是 JSON 值");
}
const suppressionValueDecoder: d.Decoder<SuppressionValue> = (input, path) => {
  if (typeof input !== "object" || input === null || Array.isArray(input)) throw new d.DecodeError(path, "应为对象");
  return jsonValue(input, path, 0) as SuppressionValue;
};

const suppressionDecoder = d.object({
  created_at: d.timestamp(),
  created_by: d.uuid(),
  id: d.uuid(),
  operation_id: d.nullable(d.uuid()),
  subject: d.string({ min: 1, max: 64, pattern: /^[a-z0-9_]+$/ }),
  target_id: d.uuid(),
  target_type: d.oneOf(["release", "track", "release_group"] as const),
  value: suppressionValueDecoder,
  value_hash: d.string({ min: 1, max: 128 }),
});
const suppressionPageDecoder = d.object({ items: d.array(suppressionDecoder, { max: MAX_PAGE_ITEMS }), pagination: paginationDecoder });

export const decodeCurationSettings = d.defineDecoder(settingsDecoder);
export const decodeCurationSources = d.defineDecoder(sourcesDecoder);
export const decodeCurationSource = d.defineDecoder(sourceDecoder);
export const decodeCurationProgress = d.defineDecoder(progressDecoder);
export const decodeCurationBatch = d.defineDecoder(batchDecoder);
export const decodeCurationBatchPage = d.defineDecoder(batchPageDecoder);
export const decodeScopeRevert = d.defineDecoder(scopeRevertDecoder);
export const decodeSuppressionPage = d.defineDecoder(suppressionPageDecoder);

export type CurationSettingsDTO = d.Infer<typeof settingsDecoder>;
export type CurationSourceDTO = d.Infer<typeof sourceDecoder>;
export type CurationSourcesDTO = d.Infer<typeof sourcesDecoder>;
export type CurationProgressDTO = d.Infer<typeof progressDecoder>;
export type CurationBatchDTO = d.Infer<typeof batchDecoder>;
export type CurationBatchPageDTO = d.Infer<typeof batchPageDecoder>;
export type ScopeRevertDTO = d.Infer<typeof scopeRevertDecoder>;
export type SuppressionDTO = d.Infer<typeof suppressionDecoder>;
export type SuppressionPageDTO = d.Infer<typeof suppressionPageDecoder>;
