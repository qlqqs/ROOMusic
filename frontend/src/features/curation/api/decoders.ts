import { decode as d } from "../../../shared/api";

// 自动整理的管理员接口（M7 A、B1、C、D；合同 backend curation-contracts.md 第 7、11、15、17 节）。
// 枚举用 oneOf：后端新增取值时样本契约测试先失败，在这里补上才算确认前端已经知道。

const MAX_PAGE_ITEMS = 100;
const paginationDecoder = d.object({
  page: d.integer({ min: 1 }),
  page_size: d.integer({ min: 1, max: MAX_PAGE_ITEMS }),
  total: d.integer({ min: 0 }),
});

const userRefDecoder = d.object({ id: d.uuid(), username: d.string({ max: 200 }) });

// 总开关：enabled 是开关本身，active 是“现在自动写入会不会被接受”；enabled_at 在从未打开过时为 null，关掉之后保留打开时的值。
// model 是整理用的模型（空串 = 开启者的对话模型）；daily_token_limit 是整理与艺人联网共用的每天 token 上限（null = 不限，默认 1000 万；ADR 0009 第 6 节）。
const settingsDecoder = d.object({
  active: d.boolean(),
  daily_token_limit: d.nullable(d.integer({ min: 1 })),
  // 一次请求最多发送的 token（估算，含候选与说明），超出时分批；1 到 10¹²，默认 128000。
  max_request_tokens: d.integer({ min: 1, max: 1_000_000_000_000 }),
  // 同时运行的模型任务数（整理缺口 R24），1–16。
  model_concurrency: d.integer({ min: 1, max: 16 }),
  model: d.string({ max: 200 }),
  enabled: d.boolean(),
  enabled_at: d.nullable(d.timestamp()),
  enabled_by: d.nullable(userRefDecoder),
  inactive_reason: d.nullable(d.oneOf(["disabled", "enabler_unavailable"] as const)),
  paused: d.boolean(),
});

export const SOURCE_CAPABILITIES = ["release_lookup", "recording_lookup", "artist_lookup", "cover_art", "lyrics", "fingerprint_lookup", "artist_image", "artist_bio"] as const;
export type SourceCapability = (typeof SOURCE_CAPABILITIES)[number];

export const SOURCE_INACTIVE_REASONS = ["curation_disabled", "enabler_unavailable", "source_disabled", "fingerprint_unavailable", "bundled_key_rejected", "key_missing", "key_invalid", "search_unconfigured"] as const;
export type SourceInactiveReason = (typeof SOURCE_INACTIVE_REASONS)[number];

// 需要密钥的源（AcoustID：发布镜像内置应用密钥时只有用户密钥，自行构建的没有内置时是应用密钥与用户密钥）才有 keys，按声明顺序每把一项：只给出有没有、末四位与服务端用过之后的结论（unchecked 是还没用过）。
const sourceKeyDecoder = d.object({
  configured: d.boolean(),
  last_four: d.string({ max: 4 }),
  name: d.string({ min: 1, max: 32, pattern: /^[a-z_]+$/ }),
  // 可选的密钥（Fanart.tv 的个人密钥；发布镜像内置了项目密钥时项目密钥也是）：不填也能用。
  optional: d.optional(d.boolean()),
  status: d.oneOf(["unchecked", "valid", "invalid"] as const),
});

// 一个外部源：enabled 是它自己的开关，active 是“后台现在能不能用它”；不能用时 inactive_reason 依次是总开关、开启者、源自己的开关、
// 指纹工具、有密钥没填、有密钥无效。
const sourceDecoder = d.object({
  active: d.boolean(),
  capabilities: d.array(d.oneOf(SOURCE_CAPABILITIES), { min: 1, max: SOURCE_CAPABILITIES.length }),
  enabled: d.boolean(),
  // 插件源的标识是 plugin:<插件 id>（M8）。
  id: d.string({ min: 1, max: 64, pattern: /^[a-z0-9._:-]+$/ }),
  inactive_reason: d.nullable(d.oneOf(SOURCE_INACTIVE_REASONS)),
  keys: d.optional(d.array(sourceKeyDecoder, { min: 1, max: 8 })),
  name: d.string({ min: 1, max: 200 }),
  // 网络搜索才有：它依赖的对话模型与搜索来源（都在开启者的管家设置里）是否就绪。
  requirements: d.optional(d.nullable(d.array(d.object({ kind: d.oneOf(["model", "search"] as const), ready: d.boolean() }), { min: 1, max: 4 }))),
  // 插件源才有：插件会访问的上游地址（来自它的清单）。
  upstreams: d.optional(d.array(d.string({ min: 1, max: 2048 }), { max: 16 })),
});
const sourcesDecoder = d.object({ items: d.array(sourceDecoder, { max: 50 }) });

export const PROGRAM_STATES = ["running", "waiting_for_scan", "idle", "paused", "disabled", "unavailable"] as const;
export type ProgramState = (typeof PROGRAM_STATES)[number];

const count = () => d.integer({ min: 0 });

export const MODEL_STATES = ["idle", "running", "waiting_review", "paused", "disabled", "waiting_for_scan", "limit_reached", "unavailable"] as const;
export type ModelState = (typeof MODEL_STATES)[number];
export const MODEL_UNAVAILABLE_REASONS = ["not_configured", "key_needs_reentry", "tool_unsupported", "enabler_unavailable", "source_disabled", "provider_error"] as const;
export type ModelUnavailableReason = (typeof MODEL_UNAVAILABLE_REASONS)[number];
/**
 * 模型暂停的原因（ADR 0009 第 6.5 节）：configuration 是配置被拒（密钥、模型名、不支持工具），改配置或手动测试成功才恢复；
 * provider_quota 是上游额度或余额不足，每小时自动探测一次；temporary 是上游临时出错，按退避或上游给的时刻再试；
 * empty_replies 是连续空返回；daily_limit 是本地的每日上限，下个日界恢复。null 表示没有暂停。
 */
export const MODEL_PAUSE_KINDS = ["configuration", "provider_quota", "temporary", "empty_replies", "daily_limit"] as const;
export type ModelPauseKind = (typeof MODEL_PAUSE_KINDS)[number];

const usageDecoder = d.object({ date: d.string({ pattern: /^\d{4}-\d{2}-\d{2}$/ }), input_tokens: count(), output_tokens: count() });
// 模型层（M7 D）：状态与不可用的原因、今天（服务器时区）的用量与上限、待定项各状态的数目（只算可见发行）。
const modelProgressDecoder = d.object({
  // 当前同时在跑的项数与并发上限（R24）。
  concurrency: d.integer({ min: 1, max: 16 }),
  running: count(),
  daily_token_limit: d.nullable(d.integer({ min: 1 })),
  items: d.object({ in_progress: count(), shelved: count(), waiting_human: count(), waiting_model: count() }),
  model: d.string({ max: 200 }),
  state: d.oneOf(MODEL_STATES),
  today: usageDecoder,
  unavailable_reason: d.nullable(d.oneOf(MODEL_UNAVAILABLE_REASONS)),
  // 有没有可用的模型配置（开关状态另看 state）。
  configured: d.boolean(),
  pause_kind: d.nullable(d.oneOf(MODEL_PAUSE_KINDS)),
  // 暂停原因的说明（已脱敏的上游原文或本地说明），没有时为 null。
  reason_detail: d.nullable(d.string({ max: 2000 })),
  // 自动再试或恢复的时刻（上游额度的下次探测、退避到期、下个日界），没有时为 null。
  retry_at: d.nullable(d.timestamp()),
  // 现在能不能手动测试一次连接（POST /admin/curation/model/retry）。
  can_retry: d.boolean(),
  // 设置“单次最多发送”的提示（例如本机模型按实际加载的上下文长度设置），没有时为 null。
  context_hint: d.nullable(d.string({ max: 2000 })),
});
// 五条程序队列的进度（整理链 B）：固定顺序为识别、封面、歌词、艺人资料、声纹。remaining 含在途与等待中的项，ready 是现在能领取的；
// 速度按最近 5 分钟实测，没有样本时为 null；来源被限流或不可用时带来源与预计恢复时刻。
export const PROGRAM_QUEUE_IDS = ["release", "artwork", "lyrics", "artist_media", "fingerprint"] as const;
// unavailable：没有装配程序层（例如测试或关闭了程序整理）时的占位状态。
export const QUEUE_STATES = ["running", "idle", "paused", "disabled", "waiting_for_scan", "unavailable"] as const;
export const QUEUE_PAUSE_REASONS = [
  "disabled",
  "paused",
  "waiting_for_scan",
  "waiting_for_stability",
  "waiting_for_fingerprint",
  "source_disabled",
  "rate_limited",
  "source_unavailable",
] as const;
const rate = () => d.nullable(d.number({ min: 0 }));
const queueDecoder = d.object({
  id: d.oneOf(PROGRAM_QUEUE_IDS),
  state: d.oneOf(QUEUE_STATES),
  remaining: count(),
  ready: count(),
  successful_requests_per_second: rate(),
  completed_items_per_second: rate(),
  eta_seconds: d.nullable(count()),
  pause_reason: d.nullable(d.oneOf(QUEUE_PAUSE_REASONS)),
  source: d.nullable(d.string({ min: 1, max: 64 })),
  retry_at: d.nullable(d.timestamp()),
});
// 后台的进度：releases 只算可见发行；processed 是按当前规则版本处理过的（输入变了、还没重新处理的仍算在内）。
const progressDecoder = d.object({
  batch: d.nullable(d.object({ id: d.uuid(), opened_at: d.timestamp(), operations: count() })),
  last_pass_finished_at: d.nullable(d.timestamp()),
  model: modelProgressDecoder,
  pending: d.object({ waiting_model: count() }),
  queues: d.array(queueDecoder, { min: PROGRAM_QUEUE_IDS.length, max: PROGRAM_QUEUE_IDS.length }),
  // 识别结论（ADR 0009 第 4.6 节）：已匹配、未匹配（含候选已撤销）、有冲突；try_later 是等来源恢复后接着查的，
  // query_error 是只和这张专辑有关的错误连续 3 次后停下的（本地文件变了或规则升级才再查）。
  releases: d.object({
    matched: count(),
    unmatched: count(),
    conflict: count(),
    processed: count(),
    total: count(),
    try_later: count(),
    query_error: count(),
  }),
  rule_version: d.integer({ min: 1 }),
  state: d.oneOf(PROGRAM_STATES),
});

/** 一张发行在各来源的识别结论（ADR 0009 第 4.6 节）：各来源各记各的，互不覆盖。 */
export const MATCH_RESULT_STATUSES = ["matched", "unmatched", "conflict"] as const;
export type MatchResultStatus = (typeof MATCH_RESULT_STATUSES)[number];
// checked_at 是最近一次成功查询的时刻（UTC），历史记录不知道时为 null；reason 为 candidates_revoked 时表示候选都被撤销或被别的专辑占用；
// conflicts 是冲突在哪的代码（例如 track_count、duration_mismatch）。
const matchResultDecoder = d.object({
  source: d.string({ min: 1, max: 100 }),
  status: d.oneOf(MATCH_RESULT_STATUSES),
  checked_at: d.nullable(d.timestamp()),
  reason: d.string({ max: 200 }),
  conflicts: d.array(d.string({ min: 1, max: 100 }), { max: 50 }),
});
const matchResultsDecoder = d.object({ results: d.array(matchResultDecoder, { max: 50 }) });
/** GET /admin/curation/releases/{id}/match-results：按来源排序，没有记录时为空数组。 */
export const decodeReleaseMatchResults = d.defineDecoder(matchResultsDecoder);
export type ReleaseMatchResultDTO = d.Infer<typeof matchResultDecoder>;

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
  target_type: d.oneOf(["release", "track", "release_group", "artist"] as const),
  value: suppressionValueDecoder,
  value_hash: d.string({ min: 1, max: 128 }),
});
const suppressionPageDecoder = d.object({ items: d.array(suppressionDecoder, { max: MAX_PAGE_ITEMS }), pagination: paginationDecoder });

// 例外（M7 D）：后台审查没通过或转人工的一项提案。input 是提出时的工具输入（只取已知的键），preview.changes 是各项的前值与后值；
// 外部来的文字（候选标题、外部编号、审查理由）都是不可信文本。status：open 等处理，accepting 是某位管理员正在采纳（中断后由他续做）。
const jsonDecoder: d.Decoder<JsonValue> = (input, path) => jsonValue(input, path, 0);
export const SUGGESTION_REASONS = ["review_rejected", "review_uncertain", "verification_declined", "no_evidence", "unverified_fact"] as const;
const exceptionChangeDecoder = d.object({ after: jsonDecoder, before: jsonDecoder, field: d.string({ min: 1, max: 64 }) });
const exceptionDecoder = d.object({
  conversation_id: d.nullable(d.uuid()),
  created_at: d.timestamp(),
  decided_at: d.nullable(d.timestamp()),
  decided_by: d.nullable(d.uuid()),
  id: d.uuid(),
  input: suppressionValueDecoder,
  // release_merge（M7 H）：两个本地发行对上同一个外部发行，模型提出整张并入。artist_identity：网络搜索给没有外部对应的艺人提出的身份候选。
  // verify_*（整理链 E）：同位署名是否同一身份、两个艺人是否合并、名字是否错字的核对判断。
  kind: d.oneOf(["release_match", "recording_match", "release_merge", "artist_identity", "verify_artist_credit", "verify_artist_merge", "verify_name"] as const),
  // 目标是发行时有 release，是艺人时有 artist（另一个为 null）。
  artist: d.nullable(d.object({ id: d.uuid(), name: d.string({ max: d.unbounded }) })),
  operation_ids: d.array(d.uuid(), { max: 16 }),
  pending_item_id: d.uuid(),
  preview: d.object({ changes: d.array(exceptionChangeDecoder, { max: 100 }) }),
  release: d.nullable(d.object({ artist: d.string({ max: d.unbounded }), id: d.uuid(), title: d.string({ max: d.unbounded }) })),
  // 整理链 E 起例外改为“未采用的建议”：审查之前就结束的（没有佐证、事实无法核实等）没有 review。
  review: d.nullable(
    d.object({
      decision: d.oneOf(["rejected", "needs_human_confirmation"] as const),
      id: d.uuid(),
      model: d.string({ max: 200 }),
      reasons: d.array(d.string({ max: d.unbounded }), { max: 50 }),
      risk: d.nullable(d.oneOf(["low", "medium", "high"] as const)),
      source: d.oneOf(["model", "policy"] as const),
    }),
  ),
  // 没采用的原因；reasons 是给人看的理由（不可信文本），evidence 是后端取到的佐证；can_accept 为 false 的只能查看（例如判断为写法差异）。
  reason_code: d.oneOf(SUGGESTION_REASONS),
  reasons: d.array(d.string({ max: d.unbounded }), { max: 50 }),
  evidence: jsonDecoder,
  can_accept: d.boolean(),
  // superseded：同一位艺人的另一个身份候选被采纳，这一条随之关闭。
  status: d.oneOf(["open", "accepting", "accepted", "declined", "superseded"] as const),
  tool: d.oneOf(["resolve_release_match", "correct_release", "correct_track", "merge_releases", "resolve_artist_identity", "submit_artist_media", "submit_verification", "observation"] as const),
  // 曲目字段的提案（M7 I）带曲目（提出时的有效标题）。
  track: d.optional(d.object({ id: d.uuid(), title: d.string({ max: d.unbounded }) })),
});
const exceptionPageDecoder = d.object({
  items: d.array(exceptionDecoder, { max: MAX_PAGE_ITEMS }),
  page: d.integer({ min: 1 }),
  page_size: d.integer({ min: 1, max: MAX_PAGE_ITEMS }),
  total: d.integer({ min: 0 }),
});
const exceptionAcceptedDecoder = d.object({ exception: exceptionDecoder, operation_ids: d.array(d.uuid(), { max: 16 }) });
const exceptionDeclinedDecoder = d.object({ exception: exceptionDecoder });

// 自动整理的会话（M7 D）：归当前管理员、模式固定 steward；打开、接回与续聊用管家的会话接口。item_counts 是这个会话领取过的待定项按状态的数目（没有的状态不出现）。
const itemCount = () => d.optional(count());
const curationConversationDecoder = d.object({
  created_at: d.timestamp(),
  id: d.uuid(),
  item_counts: d.object({ in_progress: itemCount(), resolved: itemCount(), shelved: itemCount(), waiting_human: itemCount(), waiting_model: itemCount() }),
  kind: d.literal("curation"),
  last_read_at: d.nullable(d.timestamp()),
  last_run_finished_at: d.nullable(d.timestamp()),
  mode: d.literal("steward"),
  model: d.string(),
  pending_item_count: count(),
  run_status: d.oneOf(["idle", "running", "awaiting_confirmation"] as const),
  status: d.oneOf(["idle", "running"] as const),
  title: d.string(),
  unread: d.boolean(),
  updated_at: d.timestamp(),
});
const curationConversationPageDecoder = d.object({ items: d.array(curationConversationDecoder, { max: MAX_PAGE_ITEMS }), pagination: paginationDecoder });

export const decodeCurationSettings = d.defineDecoder(settingsDecoder);
export const decodeCurationSources = d.defineDecoder(sourcesDecoder);
export const decodeCurationSource = d.defineDecoder(sourceDecoder);
export const decodeCurationProgress = d.defineDecoder(progressDecoder);
/** POST /admin/curation/model/retry：手动测试一次连接，返回更新后的模型层状态。 */
export const decodeCurationModelRetry = d.defineDecoder(modelProgressDecoder);
export const decodeCurationBatch = d.defineDecoder(batchDecoder);
export const decodeCurationBatchPage = d.defineDecoder(batchPageDecoder);
export const decodeScopeRevert = d.defineDecoder(scopeRevertDecoder);
export const decodeSuppressionPage = d.defineDecoder(suppressionPageDecoder);
export const decodeCurationExceptionPage = d.defineDecoder(exceptionPageDecoder);
export const decodeCurationExceptionAccepted = d.defineDecoder(exceptionAcceptedDecoder);
export const decodeCurationExceptionDeclined = d.defineDecoder(exceptionDeclinedDecoder);
export const decodeCurationConversationPage = d.defineDecoder(curationConversationPageDecoder);

// 发行的本地分类（整理链 A）：同一快照的计数，四类之和等于 total；versions 是三种规则版本。
const classificationsDecoder = d.object({
  releases: d.object({ has_gaps: count(), insufficient: count(), local_complete: count(), tagged_id: count(), total: count() }),
  versions: d.object({ external_match_rule: d.integer({ min: 1 }), local_rule: d.integer({ min: 1 }), patrol_prompt: d.integer({ min: 1 }) }),
});
export const decodeCurationClassifications = d.defineDecoder(classificationsDecoder);

export type CurationSettingsDTO = d.Infer<typeof settingsDecoder>;
export type CurationSourceDTO = d.Infer<typeof sourceDecoder>;
export type CurationSourcesDTO = d.Infer<typeof sourcesDecoder>;
export type CurationProgressDTO = d.Infer<typeof progressDecoder>;
export type CurationQueueDTO = CurationProgressDTO["queues"][number];
export type CurationClassificationsDTO = d.Infer<typeof classificationsDecoder>;
export type CurationModelProgressDTO = d.Infer<typeof modelProgressDecoder>;
export type CurationExceptionDTO = d.Infer<typeof exceptionDecoder>;
export type CurationExceptionPageDTO = d.Infer<typeof exceptionPageDecoder>;
export type CurationExceptionChangeDTO = d.Infer<typeof exceptionChangeDecoder>;
export type CurationConversationDTO = d.Infer<typeof curationConversationDecoder>;
export type CurationConversationPageDTO = d.Infer<typeof curationConversationPageDecoder>;
export type { JsonValue };
export type CurationBatchDTO = d.Infer<typeof batchDecoder>;
export type CurationBatchPageDTO = d.Infer<typeof batchPageDecoder>;
export type ScopeRevertDTO = d.Infer<typeof scopeRevertDecoder>;
export type SuppressionDTO = d.Infer<typeof suppressionDecoder>;
export type SuppressionPageDTO = d.Infer<typeof suppressionPageDecoder>;
