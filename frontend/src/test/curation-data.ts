import type {
  CurationClassificationsDTO,
  CurationConversationDTO,
  CurationExceptionDTO,
  CurationModelProgressDTO,
  CurationProgressDTO,
  CurationQueueDTO,
  CurationSettingsDTO,
  CurationSourceDTO,
  ProgramState,
  SuppressionDTO,
} from "../features/curation/api/decoders";
import { failure, type ApiResult } from "./catalog-data";
import { operationsOf, respondToRevert, type GovernanceData } from "./governance-data";

// 自动整理管理员接口的有状态替身（Vitest 与 Playwright 共用，只依赖纯模块）。
// 批次的改动数与已撤销数由 governance 替身里的操作记录推出，范围撤销复用同一套单条撤销，
// 所以设置页、操作记录与“不再提出”三处看到的结果一致。

export type CurationData = {
  /** 操作记录（批次计数与范围撤销用）；默认替身没有时批次都是空的。 */
  governance: GovernanceData | null;
  settings: CurationSettingsDTO;
  sources: CurationSourceDTO[];
  /** 后台的内存状态（服务器的 state）；为 null 时按设置推出（开着 → idle 或 paused，关着 → disabled）。 */
  state: ProgramState | null;
  releases: CurationProgressDTO["releases"];
  batches: { id: string; openedAt: string; closedAt: string | null }[];
  suppressions: SuppressionDTO[];
  /** 范围撤销每段的条数（后端是 100；测试调小以覆盖分段）。 */
  revertPageSize: number;
  /** 服务器上的指纹工具能不能用（不能用时 AcoustID 显示 fingerprint_unavailable）。 */
  fingerprintAvailable: boolean;
  bundledKeyRejected: boolean;
  /** 模型层（M7 D）：状态为 null 时按设置推出（关着 disabled、暂停 paused，否则 idle）；今天的用量、各状态的项数。 */
  model: {
    state: CurationModelProgressDTO["state"] | null;
    unavailableReason: CurationModelProgressDTO["unavailable_reason"];
    effectiveModel: string;
    today: { input_tokens: number; output_tokens: number };
    items: CurationModelProgressDTO["items"];
    /** 同时在跑的项数；不设时为 0。 */
    running?: number;
    /** 模型暂停（ADR 0009 第 6.5 节）；不设时没有暂停。 */
    pause?: { kind: NonNullable<CurationModelProgressDTO["pause_kind"]>; detail: string | null; retryAt: string | null };
    /** 能不能手动测试连接；不设时为 false。 */
    canRetry?: boolean;
    /** 收到的“测试模型连接”请求数。 */
    retries?: number;
  };
  /** 例外（新的在前）与当前管理员名下的自动整理会话。 */
  exceptions: CurationExceptionDTO[];
  conversations: CurationConversationDTO[];
  counter: number;
  /** 本地分类的计数（整理链 A）；为 null 时全部算“有缺口”。 */
  classifications: CurationClassificationsDTO["releases"] | null;
  /** 收到的“重新巡检”请求数（整理链 D）。 */
  repatrols: number;
  /** 五条程序队列（整理链 B）；为 null 时都空闲、已排空。 */
  queues: CurationQueueDTO[] | null;
};

const ADMIN = { id: "00000000-0000-4000-8000-000000000002", username: "curator" };

export function curationDataOf(governance: GovernanceData | null = null): CurationData {
  return {
    governance,
    settings: { active: false, enabled: false, enabled_at: null, enabled_by: null, inactive_reason: "disabled", paused: false, model: "", daily_token_limit: 10_000_000, max_request_tokens: 128_000, model_concurrency: 4 },
    sources: [
      { active: false, capabilities: ["release_lookup", "recording_lookup", "artist_lookup"], enabled: true, id: "musicbrainz", inactive_reason: "curation_disabled", name: "MusicBrainz" },
      { active: false, capabilities: ["cover_art"], enabled: true, id: "coverartarchive", inactive_reason: "curation_disabled", name: "Cover Art Archive" },
      { active: false, capabilities: ["lyrics"], enabled: true, id: "lrclib", inactive_reason: "curation_disabled", name: "LRCLIB" },
      {
        active: false,
        capabilities: ["fingerprint_lookup"],
        enabled: true,
        id: "acoustid",
        inactive_reason: "curation_disabled",
        keys: [
          { configured: false, last_four: "", name: "application", status: "unchecked" },
          { configured: false, last_four: "", name: "user", status: "unchecked" },
        ],
        name: "AcoustID",
      },
    ],
    state: null,
    // 默认曲库里没有可整理的发行：现有的管理员概览测试不出现“打开自动整理”的提示。
    releases: { total: 0, processed: 0, matched: 0, uncertain: 0, no_match: 0, try_later: 0, unlisted: 0, no_usable_candidates: 0 },
    batches: [],
    suppressions: [],
    classifications: null,
    repatrols: 0,
    queues: null,
    revertPageSize: 100,
    fingerprintAvailable: true,
    bundledKeyRejected: false,
    model: { state: null, unavailableReason: null, effectiveModel: "", today: { input_tokens: 0, output_tokens: 0 }, items: { in_progress: 0, shelved: 0, waiting_human: 0, waiting_model: 0 } },
    exceptions: [],
    conversations: [],
    counter: 0,
  };
}

// 与后端相同的判断顺序：总开关、开启者、源的开关、指纹工具、内置应用密钥被拒、密钥没填、密钥无效。
function inactiveReason(data: CurationData, source: CurationSourceDTO): CurationSourceDTO["inactive_reason"] {
  if (!data.settings.enabled) return "curation_disabled";
  if (!data.settings.active) return "enabler_unavailable";
  if (!source.enabled) return "source_disabled";
  if (source.keys === undefined) return null;
  if (!data.fingerprintAvailable) return "fingerprint_unavailable";
  if (data.bundledKeyRejected) return "bundled_key_rejected";
  if (source.keys.some((key) => !key.configured)) return "key_missing";
  return source.keys.some((key) => key.status === "invalid") ? "key_invalid" : null;
}

/** 测试与替身共用：按当前设置重新推出各源的 active 与 inactive_reason。 */
export function refreshSources(data: CurationData): void {
  data.sources = data.sources.map((source) => {
    const reason = inactiveReason(data, source);
    return { ...source, active: reason === null, inactive_reason: reason };
  });
}

/** 测试用：直接把设置改成打开（由 curator 在给定时间打开），各源随之生效。 */
export function enableCuration(data: CurationData, at = "2026-01-02T00:00:00Z"): void {
  data.settings = { ...data.settings, active: true, enabled: true, enabled_at: at, enabled_by: ADMIN, inactive_reason: null, paused: false };
  refreshSources(data);
}

export function respondToCurationSettings(data: CurationData): ApiResult {
  return { status: 200, body: data.settings };
}

export function respondToUpdateCurationSettings(data: CurationData, body: unknown): ApiResult {
  const change = typeof body === "object" && body !== null ? (body as { enabled?: unknown; paused?: unknown; model?: unknown; daily_token_limit?: unknown; max_request_tokens?: unknown; model_concurrency?: unknown }) : {};
  const enabled = typeof change.enabled === "boolean" ? change.enabled : undefined;
  const paused = typeof change.paused === "boolean" ? change.paused : undefined;
  const model = typeof change.model === "string" ? change.model : undefined;
  const hasLimit = "daily_token_limit" in change;
  const limit = change.daily_token_limit;
  if (hasLimit && limit !== null && !(typeof limit === "number" && Number.isInteger(limit) && limit >= 1 && limit <= 1e12)) return failure(400, "invalid_input", "每日上限无效");
  const hasRequestSize = "max_request_tokens" in change;
  const requestSize = change.max_request_tokens;
  if (hasRequestSize && !(typeof requestSize === "number" && Number.isInteger(requestSize) && requestSize >= 1 && requestSize <= 1e12)) return failure(400, "invalid_input", "单次最多发送无效");
  const hasConcurrency = "model_concurrency" in change;
  const concurrency = change.model_concurrency;
  if (hasConcurrency && !(typeof concurrency === "number" && Number.isInteger(concurrency) && concurrency >= 1 && concurrency <= 16)) return failure(400, "invalid_input", "同时运行数无效");
  if (model !== undefined && new TextEncoder().encode(model).length > 200) return failure(400, "invalid_input", "模型名太长");
  if (enabled === undefined && paused === undefined && model === undefined && !hasLimit && !hasRequestSize && !hasConcurrency) return failure(400, "invalid_input", "至少要改一项设置");
  const settings = { ...data.settings };
  if (model !== undefined) settings.model = model;
  if (hasLimit) settings.daily_token_limit = limit as number | null;
  if (hasRequestSize) settings.max_request_tokens = requestSize as number;
  if (hasConcurrency) settings.model_concurrency = concurrency as number;
  if (enabled !== undefined) {
    if (enabled && !settings.enabled) {
      settings.enabled_by = ADMIN;
      data.counter += 1;
      settings.enabled_at = new Date(Date.UTC(2026, 0, 5) + data.counter * 1000).toISOString().replace(".000Z", "Z");
      settings.paused = false;
    }
    settings.enabled = enabled;
  }
  if (paused !== undefined && !(enabled === true && !data.settings.enabled)) settings.paused = paused;
  settings.active = settings.enabled;
  settings.inactive_reason = settings.enabled ? null : "disabled";
  data.settings = settings;
  refreshSources(data);
  return { status: 200, body: settings };
}

export function respondToCurationSources(data: CurationData): ApiResult {
  return { status: 200, body: { items: data.sources } };
}

export function respondToSetCurationSource(data: CurationData, id: string, body: unknown): ApiResult {
  const enabled = typeof body === "object" && body !== null && typeof (body as { enabled?: unknown }).enabled === "boolean" ? (body as { enabled: boolean }).enabled : undefined;
  if (enabled === undefined) return failure(400, "invalid_input", "请求体无效");
  const index = data.sources.findIndex((source) => source.id === id);
  if (index < 0) return failure(404, "not_found", "外部源不存在");
  data.sources[index] = { ...(data.sources[index] as CurationSourceDTO), enabled };
  refreshSources(data);
  return { status: 200, body: data.sources[index] };
}

/** 填写或清除一个源的某一把密钥：key 为 null 时清除；只返回末四位，状态回到“还没用过”。 */
export function respondToSetCurationSourceKey(data: CurationData, id: string, name: string, key: string | null): ApiResult {
  const index = data.sources.findIndex((source) => source.id === id);
  if (index < 0) return failure(404, "not_found", "外部源不存在");
  const source = data.sources[index] as CurationSourceDTO;
  if (source.keys === undefined) return failure(400, "key_not_supported", "这个外部源不需要密钥");
  if (!source.keys.some((item) => item.name === name)) return failure(404, "not_found", "密钥不存在");
  if (key !== null && (key.trim() === "" || key.trim().length > 128)) return failure(400, "invalid_input", "密钥无效");
  const trimmed = key?.trim() ?? "";
  const keys = source.keys.map((item) => (item.name === name ? { name, configured: key !== null, last_four: [...trimmed].slice(-4).join(""), status: "unchecked" as const } : item));
  data.sources[index] = { ...source, keys };
  refreshSources(data);
  return { status: 200, body: data.sources[index] };
}

function stateOf(data: CurationData): ProgramState {
  if (data.state !== null) return data.state;
  if (!data.settings.enabled) return "disabled";
  return data.settings.paused ? "paused" : "idle";
}

function batchCounts(data: CurationData, batchId: string): { operations: number; reverted: number } {
  if (data.governance === null) return { operations: 0, reverted: 0 };
  const records = operationsOf(data.governance).filter((record) => record.batchId === batchId);
  return { operations: records.length, reverted: records.filter((record) => record.revertedBy !== null).length };
}

// 五条程序队列都空闲、已排空时的进度（整理链 B）。
export function idleQueues() {
  return (["release", "artwork", "lyrics", "artist_media", "fingerprint"] as const).map((id) => ({
    id,
    state: "idle" as const,
    remaining: 0,
    ready: 0,
    successful_requests_per_second: null,
    completed_items_per_second: null,
    eta_seconds: 0,
    pause_reason: null,
    source: null,
    retry_at: null,
  }));
}

/** 本地分类的计数：没有指定时全部算“有缺口”，四类之和等于 total。 */
export function respondToCurationClassifications(data: CurationData): ApiResult {
  const total = data.releases.total;
  return {
    status: 200,
    body: {
      releases: data.classifications ?? { total, tagged_id: 0, has_gaps: total, local_complete: 0, insufficient: 0 },
      versions: { local_rule: 2, external_match_rule: 102, patrol_prompt: 1 },
    },
  };
}

/** 测试模型连接：记一次；能测试时解除暂停，返回更新后的模型层状态。 */
export function respondToRetryCurationModel(data: CurationData): ApiResult {
  data.model.retries = (data.model.retries ?? 0) + 1;
  if (data.model.canRetry === true) {
    delete data.model.pause;
    data.model.canRetry = false;
  }
  return { status: 200, body: modelProgressOf(data) };
}

/** 重新巡检：只记一次，返回 202 空体。 */
export function respondToRepatrol(data: CurationData): ApiResult {
  data.repatrols += 1;
  return { status: 202, body: null };
}

export function respondToCurationProgress(data: CurationData): ApiResult {
  const open = data.batches.find((batch) => batch.closedAt === null);
  return {
    status: 200,
    body: {
      batch: open === undefined ? null : { id: open.id, opened_at: open.openedAt, operations: batchCounts(data, open.id).operations },
      last_pass_finished_at: stateOf(data) === "idle" && data.settings.enabled ? "2026-01-05T00:10:00Z" : null,
      model: modelProgressOf(data),
      pending: { waiting_model: data.model.items.waiting_model },
      queues: data.queues ?? idleQueues(),
      releases: data.releases,
      rule_version: 101,
      state: stateOf(data),
    },
  };
}

function batchBody(data: CurationData, batch: CurationData["batches"][number]) {
  return { closed_at: batch.closedAt, id: batch.id, opened_at: batch.openedAt, ...batchCounts(data, batch.id), tier: "program" as const };
}

function paged<T>(items: readonly T[], query: URLSearchParams): ApiResult | { page: number; pageSize: number; slice: T[] } {
  const page = Number(query.get("page") ?? "1");
  const pageSize = Number(query.get("page_size") ?? "50");
  if (!Number.isInteger(page) || page < 1 || !Number.isInteger(pageSize) || pageSize < 1 || pageSize > 100) return failure(400, "invalid_pagination", "分页参数无效");
  const start = (page - 1) * pageSize;
  return { page, pageSize, slice: items.slice(start, start + pageSize) };
}

export function respondToCurationBatches(data: CurationData, query: URLSearchParams): ApiResult {
  const ordered = [...data.batches].sort((a, b) => b.openedAt.localeCompare(a.openedAt));
  const result = paged(ordered, query);
  if ("status" in result) return result;
  return { status: 200, body: { items: result.slice.map((batch) => batchBody(data, batch)), pagination: { page: result.page, page_size: result.pageSize, total: ordered.length } } };
}

export function respondToCurationBatch(data: CurationData, id: string): ApiResult {
  const batch = data.batches.find((item) => item.id === id);
  return batch === undefined ? failure(404, "not_found", "批次不存在") : { status: 200, body: batchBody(data, batch) };
}

/** 测试用：开一个批次（closedAt 为 null 表示进行中）。 */
export function seedBatch(data: CurationData, batch: { id: string; openedAt: string; closedAt?: string | null }): void {
  data.batches.push({ id: batch.id, openedAt: batch.openedAt, closedAt: batch.closedAt === undefined ? "2026-01-05T01:00:00Z" : batch.closedAt });
}

/**
 * 范围撤销的一段：候选是范围里不是撤销、还没被撤销的记录，按时间倒序，从游标之后开始；每条用请求键与记录 ID 派生的键撤销（与后端一样，
 * 同键重试不会重复撤销）。游标是上一段最后一条的记录 ID。
 */
export function respondToScopeRevert(data: CurationData, key: string | null, body: unknown): ApiResult {
  if (key === null) return failure(400, "invalid_input", "请求需要 Idempotency-Key（UUID）");
  const request = typeof body === "object" && body !== null ? (body as { scope?: { kind?: unknown; id?: unknown; provider?: unknown }; cursor?: unknown }) : {};
  const scope = request.scope;
  const matches =
    scope?.kind === "batch" && typeof scope.id === "string"
      ? (record: { batchId?: string }) => record.batchId === scope.id
      : scope?.kind === "provider" && typeof scope.provider === "string"
        ? (record: { origin?: { source: string; provider?: string | null } }) => record.origin?.source === "curation" && record.origin.provider === scope.provider
        : null;
  if (matches === null) return failure(400, "invalid_input", "范围无效");
  const governance = data.governance;
  if (governance === null) return { status: 200, body: { next_cursor: null, reverted: 0, skipped: [] } };
  const all = [...operationsOf(governance)].reverse().filter((record) => record.type !== "revert" && matches(record as never));
  const cursor = typeof request.cursor === "string" ? request.cursor : null;
  const after = cursor === null ? all : all.slice(all.findIndex((record) => record.id === cursor) + 1);
  const candidates = after.filter((record) => record.revertedBy === null).slice(0, data.revertPageSize);
  let reverted = 0;
  const skipped: { operation_id: string; code: string }[] = [];
  for (const record of candidates) {
    const result = respondToRevert(governance, record.id, `${key}:${record.id}`);
    if (result.status === 200) reverted += 1;
    else {
      const code = (result.body as { error?: { code?: string } } | null)?.error?.code ?? "operation_failed";
      skipped.push({ operation_id: record.id, code });
    }
  }
  const last = candidates.at(-1);
  const more = last !== undefined && after.filter((record) => record.revertedBy === null && !candidates.includes(record)).length > 0;
  return { status: 200, body: { next_cursor: more && last !== undefined ? last.id : null, reverted, skipped } };
}

export function respondToSuppressions(data: CurationData, query: URLSearchParams): ApiResult {
  const ordered = [...data.suppressions].sort((a, b) => b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id));
  const result = paged(ordered, query);
  if ("status" in result) return result;
  return { status: 200, body: { items: result.slice, pagination: { page: result.page, page_size: result.pageSize, total: ordered.length } } };
}

/** 清除一条标记：封面与歌词的一对一起清除（同一目标、同一操作）。 */
export function respondToClearSuppression(data: CurationData, id: string): ApiResult {
  const target = data.suppressions.find((item) => item.id === id);
  if (target === undefined) return failure(404, "not_found", "不再提出标记不存在");
  const pair: Record<string, string> = { artwork: "artwork_source", artwork_source: "artwork", lyrics: "lyrics_source", lyrics_source: "lyrics" };
  const partner = pair[target.subject];
  data.suppressions = data.suppressions.filter(
    (item) => item.id !== id && !(partner !== undefined && target.operation_id !== null && item.subject === partner && item.target_id === target.target_id && item.operation_id === target.operation_id),
  );
  return { status: 204, body: null };
}

/** 测试用：加一条标记。 */
export function seedSuppression(data: CurationData, item: Omit<SuppressionDTO, "value_hash" | "created_by"> & { value_hash?: string }): void {
  data.suppressions.push({ ...item, value_hash: item.value_hash ?? "0".repeat(63) + String(data.suppressions.length + 1), created_by: ADMIN.id });
}

function modelProgressOf(data: CurationData): CurationModelProgressDTO {
  const state = data.model.state ?? (!data.settings.enabled ? "disabled" : data.settings.paused ? "paused" : "idle");
  return {
    concurrency: data.settings.model_concurrency,
    running: data.model.running ?? 0,
    daily_token_limit: data.settings.daily_token_limit,
    items: data.model.items,
    model: state === "idle" || state === "running" ? data.settings.model || data.model.effectiveModel : "",
    state,
    today: { date: "2026-01-05", ...data.model.today },
    unavailable_reason: state === "unavailable" ? data.model.unavailableReason : null,
    configured: !(state === "unavailable" && data.model.unavailableReason === "not_configured"),
    pause_kind: data.model.pause?.kind ?? null,
    reason_detail: data.model.pause?.detail ?? null,
    retry_at: data.model.pause?.retryAt ?? null,
    can_retry: data.model.canRetry ?? false,
    context_hint: "使用本机模型时，请按模型服务实际加载的上下文长度设置单次最多发送。",
  };
}

/** 测试用：一条待处理的例外（字段修正或匹配）。 */
export function seedException(data: CurationData, patch: Partial<CurationExceptionDTO> = {}): CurationExceptionDTO {
  data.counter += 1;
  const n = String(data.counter).padStart(12, "0");
  const exception: CurationExceptionDTO = {
    artist: null,
    conversation_id: `00000000-0000-4000-9000-${n}`,
    created_at: "2026-01-05T08:00:00Z",
    decided_at: null,
    decided_by: null,
    id: `00000000-0000-4000-a000-${n}`,
    reason_code: "review_rejected",
    reasons: [],
    evidence: {},
    can_accept: true,
    input: { release_id: `00000000-0000-4000-b000-${n}`, changes: { title: { mode: "set", value: "新标题" } }, expected_revision: 1, basis: { note: "外部资料" } },
    kind: "release_match",
    operation_ids: [],
    pending_item_id: `00000000-0000-4000-c000-${n}`,
    preview: { changes: [{ field: "title", before: "旧标题", after: "新标题" }] },
    release: { id: `00000000-0000-4000-b000-${n}`, title: "旧标题", artist: "合成艺人" },
    review: { decision: "needs_human_confirmation", id: `00000000-0000-4000-d000-${n}`, model: "review-model", reasons: ["标题需要人工核对"], risk: "medium", source: "model" },
    status: "open",
    tool: "correct_release",
    ...patch,
  };
  data.exceptions.unshift(exception);
  data.model.items = { ...data.model.items, waiting_human: data.model.items.waiting_human + 1 };
  return exception;
}

export function respondToCurationExceptions(data: CurationData, search: URLSearchParams): ApiResult {
  const status = search.get("status") ?? "open";
  const page = Number(search.get("page") ?? "1");
  const pageSize = Number(search.get("page_size") ?? "50");
  const rows = data.exceptions.filter((item) => status === "all" || item.status === "open");
  return { status: 200, body: { items: rows.slice((page - 1) * pageSize, page * pageSize), page, page_size: pageSize, total: rows.length } };
}

function decide(data: CurationData, id: string, status: "accepted" | "declined"): { decided: CurationExceptionDTO } | { error: ApiResult } {
  const index = data.exceptions.findIndex((item) => item.id === id);
  const current = data.exceptions[index];
  if (current === undefined) return { error: failure(404, "not_found", "例外不存在") };
  if (current.status !== "open") return { error: failure(409, "exception_decided", "例外已经有了结论") };
  data.counter += 1;
  const operation = `00000000-0000-4000-e000-${String(data.counter).padStart(12, "0")}`;
  const decided: CurationExceptionDTO = { ...current, status, decided_by: ADMIN.id, decided_at: "2026-01-05T09:00:00Z", operation_ids: status === "accepted" ? [operation] : [] };
  data.exceptions[index] = decided;
  data.model.items = { ...data.model.items, waiting_human: Math.max(0, data.model.items.waiting_human - 1) };
  return { decided };
}

export function respondToAcceptException(data: CurationData, id: string): ApiResult {
  const result = decide(data, id, "accepted");
  return "error" in result ? result.error : { status: 200, body: { exception: result.decided, operation_ids: result.decided.operation_ids } };
}

export function respondToDeclineException(data: CurationData, id: string): ApiResult {
  const result = decide(data, id, "declined");
  return "error" in result ? result.error : { status: 200, body: { exception: result.decided } };
}

export function respondToCurationConversations(data: CurationData, search: URLSearchParams): ApiResult {
  const page = Number(search.get("page") ?? "1");
  const pageSize = Number(search.get("page_size") ?? "20");
  return { status: 200, body: { items: data.conversations.slice((page - 1) * pageSize, page * pageSize), pagination: { page, page_size: pageSize, total: data.conversations.length } } };
}
