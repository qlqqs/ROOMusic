import type {
  CurationProgressDTO,
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
  waitingModel: number;
  batches: { id: string; openedAt: string; closedAt: string | null }[];
  suppressions: SuppressionDTO[];
  /** 范围撤销每段的条数（后端是 100；测试调小以覆盖分段）。 */
  revertPageSize: number;
  /** 服务器上的指纹工具能不能用（不能用时 AcoustID 显示 fingerprint_unavailable）。 */
  fingerprintAvailable: boolean;
  bundledKeyRejected: boolean;
  counter: number;
};

const ADMIN = { id: "00000000-0000-4000-8000-000000000002", username: "curator" };

export function curationDataOf(governance: GovernanceData | null = null): CurationData {
  return {
    governance,
    settings: { active: false, enabled: false, enabled_at: null, enabled_by: null, inactive_reason: "disabled", paused: false },
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
    releases: { total: 0, processed: 0, matched: 0, uncertain: 0, no_match: 0, try_later: 0 },
    waitingModel: 0,
    batches: [],
    suppressions: [],
    revertPageSize: 100,
    fingerprintAvailable: true,
    bundledKeyRejected: false,
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
  data.settings = { active: true, enabled: true, enabled_at: at, enabled_by: ADMIN, inactive_reason: null, paused: false };
  refreshSources(data);
}

export function respondToCurationSettings(data: CurationData): ApiResult {
  return { status: 200, body: data.settings };
}

export function respondToUpdateCurationSettings(data: CurationData, body: unknown): ApiResult {
  const change = typeof body === "object" && body !== null ? (body as { enabled?: unknown; paused?: unknown }) : {};
  const enabled = typeof change.enabled === "boolean" ? change.enabled : undefined;
  const paused = typeof change.paused === "boolean" ? change.paused : undefined;
  if (enabled === undefined && paused === undefined) return failure(400, "invalid_input", "至少要改一项设置");
  const settings = { ...data.settings };
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

export function respondToCurationProgress(data: CurationData): ApiResult {
  const open = data.batches.find((batch) => batch.closedAt === null);
  return {
    status: 200,
    body: {
      batch: open === undefined ? null : { id: open.id, opened_at: open.openedAt, operations: batchCounts(data, open.id).operations },
      last_pass_finished_at: stateOf(data) === "idle" && data.settings.enabled ? "2026-01-05T00:10:00Z" : null,
      pending: { waiting_model: data.waitingModel },
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
