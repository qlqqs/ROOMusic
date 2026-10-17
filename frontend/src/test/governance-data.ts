import type { ReleaseGroupSummaryDTO, ReleaseSummaryDTO } from "../features/catalog/api/decoders";
import {
  CORRECTABLE_FIELDS,
  REVIEWABLE_ISSUES,
  type CorrectableField,
  type CorrectionFieldDTO,
  type ClaimDetailDTO,
  type ClaimEffect,
  type ClaimEntryDTO,
  type ClaimItemDTO,
  type ClaimPreviewDTO,
  type CorrectionStateDTO,
  type DefaultVersionDTO,
  type FieldIssueDTO,
  type IssueCountsDTO,
  type IssueListItemDTO,
  type OperationChangesDTO,
  type OperationItemDTO,
  type ReleaseIssuesDTO,
  type ReviewableIssue,
} from "../features/governance/api/decoders";
import { failure, groupDetailFor, releaseDetailFor, releaseSummaryFor, type ApiResult, type CatalogData } from "./catalog-data";

// 受治理操作（字段修正、人工归组、默认版本、撤销、操作记录）的接口行为，依据后端 backend/internal/catalog 的 corrections.go、group_change.go、
// group_default.go、revert.go 与 operations/httpapi：预期版本、幂等重放、同键异义、no_changes、same_group、already_alone、not_in_group、revert_conflict……
// 修改会反映到共用的专辑数据（列表、组详情、版本详情），所以页面在写操作之后读到的是新的有效值。
// 与 catalog-data.ts 一样只依赖解码器与样本读取，不依赖 vitest 或应用代码：Vitest 的替身（fake-governance.ts）与 Playwright 的受控后端共用。

type Intent = { mode: "set"; value: string | number } | { mode: "null" } | null;
type Scalar = string | number | null;

type ReleaseState = {
  /** 扫描值（专辑艺人是页面显示的值：没有专辑艺人时是艺人）。 */
  scan: Record<CorrectableField, Scalar>;
  corrections: Record<CorrectableField, Intent>;
  uncertain: Set<CorrectableField>;
  revision: number;
  /** 展示艺人的扫描值：专辑艺人有修正时展示艺人跟随它。 */
  baseArtist: string;
};

type OperationType = "release_correction" | "release_group_change" | "release_group_default" | "release_issue_review" | "release_claim" | "revert";

/** 记录的目标：发行（修正、归组）或专辑（默认版本）。 */
type OperationTarget = { type: "release" | "release_group"; id: string };

type OperationRecord = {
  id: string;
  type: OperationType;
  createdAt: string;
  target: OperationTarget;
  /** 目标在这条记录之后的人工修改版本号（发行或专辑各有自己的）。 */
  resultRevision: number;
  reverts: string | null;
  revertedBy: string | null;
  changes: OperationChangesDTO;
  /** 撤销时把发行恢复成这条操作之前的状态。 */
  restore: () => void;
};

/** 专辑自己的状态：人工修改版本号（设定、清除默认版本与撤销递增；修正与归组不改它）与已存的默认版本（可能是失效中的，即不是这个专辑的成员）。 */
type GroupState = { revision: number; defaultReleaseId: string | null };

/**
 * 一个版本的问题（扫描结论）：拿不准的字段（值、原因与候选）、归组冲突（涉及的文件）与缺失曲目。依据令牌由内容算出：
 * 内容不变令牌不变，scanIssues 改了内容就是“扫描之后情况变了”。核对记住核对时的令牌与操作，令牌不同时问题重新是待处理。
 */
export type IssueSeed = {
  fields?: readonly { field: string; value: string | null; reason: string; candidates: readonly string[] }[];
  grouping?: { sourceRefs: readonly string[]; siblings?: readonly { releaseId: string; groupId: string; title: string }[] } | null;
  missing?: readonly { id: string; title: string; medium: number; position: number; source: string; lastSeenAt: string }[];
};

type IssueReview = { basis: string; operationId: string; reviewedAt: string };
/** 缺失曲目的核对逐首记住：核对时每首曲目的“最后一次看到”的时间。 */
type MissingReview = IssueReview & { tracks: ReadonlyMap<string, string> };

type IssueState = { seed: IssueSeed; reviews: Map<ReviewableIssue, IssueReview | MissingReview> };

export type GovernanceData = {
  catalog: CatalogData;
  /** 记录里的操作者。 */
  actor: { id: string; username: string };
  releases: Map<string, ReleaseState>;
  groups: Map<string, GroupState>;
  /** 有问题的版本（测试用 seedIssues 预置）；没有登记的版本没有问题，“需要关注”取专辑数据里的值。 */
  issues: Map<string, IssueState>;
  /** 待认领的旧版本（测试用 seedClaim 预置）：已不可见、留有人工修改，值就是接口的详情响应；列表项从详情截取。 */
  claims: Map<string, ClaimDetailDTO>;
  /** 旧版本上迁移的核对在新版本上的效果（预览里的 effect）；没有登记的问题按 no_issue。 */
  claimEffects: Map<string, Partial<Record<ReviewableIssue, ClaimEffect>>>;
  /** 按发生的先后顺序保存；列表时倒序。 */
  operations: OperationRecord[];
  idempotency: Map<string, { fingerprint: string; result: ApiResult }>;
  counter: number;
};

export function governanceDataOf(catalog: CatalogData): GovernanceData {
  return {
    catalog,
    actor: { id: "00000000-0000-4000-8000-000000000002", username: "curator" },
    releases: new Map(),
    groups: new Map(),
    issues: new Map(),
    claims: new Map(),
    claimEffects: new Map(),
    operations: [],
    idempotency: new Map(),
    counter: 0,
  };
}

const uuidOf = (prefix: string, index: number) => `00000000-0000-4000-8000-${prefix}${String(index).padStart(11, "0")}`;
const emptyCorrections = (): Record<CorrectableField, Intent> => ({ title: null, album_artist: null, year: null, genre: null, label: null, catalog_number: null, edition: null, barcode: null });

/** 发行的修正状态：第一次用到时按专辑数据里的详情建立（详情里的值就是扫描值）。不存在或不可见返回 undefined。 */
function stateOf(data: GovernanceData, releaseId: string): ReleaseState | undefined {
  const existing = data.releases.get(releaseId);
  if (existing !== undefined) return existing;
  const detail = releaseDetailFor(data.catalog, releaseId);
  if (detail === undefined) return undefined;
  const state: ReleaseState = {
    scan: {
      title: detail.title,
      album_artist: detail.album_artist ?? detail.artist,
      year: detail.year,
      genre: detail.genre,
      label: detail.label,
      catalog_number: detail.catalog_number,
      edition: detail.edition,
      barcode: detail.barcode,
    },
    corrections: emptyCorrections(),
    uncertain: new Set(),
    revision: detail.revision,
    baseArtist: detail.artist,
  };
  data.releases.set(releaseId, state);
  return state;
}

/** 测试预置：把这些字段标为“扫描结果不确定”。 */
export function markUncertain(data: GovernanceData, releaseId: string, fields: readonly CorrectableField[]): void {
  const state = stateOf(data, releaseId);
  if (state === undefined) throw new Error(`没有发行 ${releaseId}`);
  for (const field of fields) state.uncertain.add(field);
}

function effectiveOf(state: ReleaseState, field: CorrectableField): Scalar {
  const intent = state.corrections[field];
  if (intent === null) return state.scan[field];
  return intent.mode === "null" ? null : intent.value;
}

function stateDTO(releaseId: string, state: ReleaseState): CorrectionStateDTO {
  return {
    release_id: releaseId,
    revision: state.revision,
    fields: CORRECTABLE_FIELDS.map(
      (field): CorrectionFieldDTO => {
        const intent = state.corrections[field];
        return {
          field,
          scan_value: state.scan[field],
          correction: intent === null ? null : intent.mode === "null" ? { mode: "null" } : { mode: "set", value: intent.value },
          effective_value: effectiveOf(state, field),
          uncertain: state.uncertain.has(field),
        };
      },
    ),
  };
}

const correctedFieldsOf = (state: ReleaseState): CorrectableField[] => CORRECTABLE_FIELDS.filter((field) => state.corrections[field] !== null);

// ---- 专辑数据里的组与版本 ----

const lower = (text: string) => text.toLowerCase();

/** 代表版本规则：年份最早（没有年份排最后），再按 ID。 */
function memberOrder(a: ReleaseSummaryDTO, b: ReleaseSummaryDTO): number {
  if ((a.year === null) !== (b.year === null)) return a.year === null ? 1 : -1;
  if (a.year !== null && b.year !== null && a.year !== b.year) return a.year - b.year;
  return a.id.localeCompare(b.id);
}

/**
 * 专辑的状态：第一次用到时按专辑数据里登记的组详情建立（版本号与生效中的默认版本），没有登记的专辑从版本号 1、没有默认版本起。
 * 必须在改写组详情之前调用，否则读到的是改写之后的值。
 */
function groupStateOf(data: GovernanceData, groupId: string): GroupState {
  const existing = data.groups.get(groupId);
  if (existing !== undefined) return existing;
  const detail = data.catalog.groupDetails.get(groupId);
  const state: GroupState = { revision: detail?.revision ?? 1, defaultReleaseId: detail?.default_release_id ?? null };
  data.groups.set(groupId, state);
  return state;
}

/**
 * 写入一个组的成员：按代表版本规则排序（已存的默认版本是成员时排在最前，否则按自动规则，等于静默失效），重算代表版本、版本数
 * 与需要关注的合计，带上专辑的版本号与生效中的默认版本；成员为空时这个组从列表里消失（专辑自己的状态保留：组从不删除）。
 */
function writeGroup(data: GovernanceData, groupId: string, members: readonly ReleaseSummaryDTO[]): void {
  const { catalog } = data;
  const state = groupStateOf(data, groupId);
  const index = catalog.groups.findIndex((group) => group.id === groupId);
  if (members.length === 0) {
    if (index >= 0) catalog.groups.splice(index, 1);
    catalog.groupDetails.delete(groupId);
    return;
  }
  const sorted = [...members].sort(memberOrder);
  const defaultIndex = sorted.findIndex((member) => member.id === state.defaultReleaseId);
  if (defaultIndex > 0) sorted.unshift(...sorted.splice(defaultIndex, 1));
  const representative = sorted[0] as ReleaseSummaryDTO;
  const summary: ReleaseGroupSummaryDTO = {
    id: groupId,
    release_count: sorted.length,
    attention_count: sorted.reduce((total, member) => total + member.attention_count, 0),
    representative,
  };
  if (index >= 0) catalog.groups[index] = summary;
  else catalog.groups.push(summary);
  catalog.groupDetails.set(groupId, { ...summary, revision: state.revision, default_release_id: defaultIndex >= 0 ? representative.id : null, releases: sorted });
}

/** 列表按代表版本的标题、艺人与 ID 排序，与后端一致。 */
function sortGroups(catalog: CatalogData): void {
  catalog.groups.sort(
    (a, b) =>
      lower(a.representative.title).localeCompare(lower(b.representative.title)) ||
      lower(a.representative.artist).localeCompare(lower(b.representative.artist)) ||
      a.representative.id.localeCompare(b.representative.id),
  );
}

function groupIdOf(catalog: CatalogData, releaseId: string): string | undefined {
  return catalog.groups.find((group) => groupDetailFor(catalog, group.id)?.releases.some((release) => release.id === releaseId) === true)?.id;
}

function membersOf(catalog: CatalogData, groupId: string): ReleaseSummaryDTO[] {
  return [...(groupDetailFor(catalog, groupId)?.releases ?? [])];
}

/** 把发行的有效值写回专辑数据的各处（列表、组详情的成员与代表版本、版本详情）。 */
function syncRelease(data: GovernanceData, releaseId: string): void {
  const state = stateOf(data, releaseId);
  const groupId = groupIdOf(data.catalog, releaseId);
  const detail = releaseDetailFor(data.catalog, releaseId);
  if (state === undefined || groupId === undefined || detail === undefined) return;
  const albumArtist = effectiveOf(state, "album_artist");
  const albumArtistText = typeof albumArtist === "string" ? albumArtist : state.baseArtist;
  const year = effectiveOf(state, "year");
  const summaryPatch = {
    title: String(effectiveOf(state, "title")),
    album_artist: albumArtistText,
    artist: state.corrections.album_artist === null ? state.baseArtist : albumArtistText,
    year: typeof year === "number" ? year : null,
  };
  writeGroup(
    data,
    groupId,
    membersOf(data.catalog, groupId).map((member) => (member.id === releaseId ? { ...member, ...summaryPatch } : member)),
  );
  const text = (field: CorrectableField): string | null => {
    const value = effectiveOf(state, field);
    return typeof value === "string" ? value : null;
  };
  data.catalog.releases.set(releaseId, {
    ...detail,
    ...summaryPatch,
    genre: text("genre"),
    label: text("label"),
    catalog_number: text("catalog_number"),
    edition: text("edition"),
    barcode: text("barcode"),
    credits: detail.credits.map((credit) => (credit.role === "album_artist" ? { ...credit, name: albumArtistText } : credit)),
    revision: state.revision,
    corrected_fields: correctedFieldsOf(state),
  });
  sortGroups(data.catalog);
}

// ---- 共同流程：幂等、记录 ----

function replayOrConflict(data: GovernanceData, key: string, fingerprint: string): ApiResult | null {
  const recorded = data.idempotency.get(key);
  if (recorded === undefined) return null;
  return recorded.fingerprint === fingerprint ? recorded.result : failure(409, "idempotency_conflict", "幂等键已用于其他请求");
}

function remember(data: GovernanceData, key: string, fingerprint: string, result: ApiResult): ApiResult {
  data.idempotency.set(key, { fingerprint, result });
  return result;
}

function nextOperation(data: GovernanceData, type: OperationType, target: OperationTarget, resultRevision: number, changes: OperationChangesDTO, restore: () => void, reverts: string | null = null): OperationRecord {
  data.counter += 1;
  const record: OperationRecord = {
    id: uuidOf("f", data.counter),
    type,
    createdAt: new Date(Date.UTC(2026, 0, 3, 0, 0, 0) + data.counter * 60_000).toISOString().replace(".000Z", "Z"),
    target,
    resultRevision,
    reverts,
    revertedBy: null,
    changes,
    restore,
  };
  data.operations.push(record);
  return record;
}

// ---- 字段修正 ----

export function respondToCorrections(data: GovernanceData, releaseId: string): ApiResult {
  const state = stateOf(data, releaseId);
  return state === undefined ? failure(404, "not_found", "发行版本不存在") : { status: 200, body: stateDTO(releaseId, state) };
}

type RawChange = { mode?: unknown; value?: unknown };
type RawCorrectionBody = { expected_revision?: unknown; changes?: Record<string, RawChange> };

/** 请求里的一项修改对应的意图：clear 是没有修正（null），修改方式不认识是 "invalid"。 */
function intentOf(change: RawChange): Intent | "invalid" {
  if (change.mode === "clear") return null;
  if (change.mode === "null") return { mode: "null" };
  if (change.mode === "set" && (typeof change.value === "string" || typeof change.value === "number")) return { mode: "set", value: change.value };
  return "invalid";
}

const sameIntent = (a: Intent, b: Intent) => (a === null || b === null ? a === b : a.mode === b.mode && (a.mode === "null" || (b.mode === "set" && a.value === b.value)));

export function respondToCorrect(data: GovernanceData, releaseId: string, key: string | null, body: unknown): ApiResult {
  const { expected_revision: expectedRevision, changes = {} } = (body ?? {}) as RawCorrectionBody;
  if (key === null) return failure(400, "invalid_input", "请求需要 Idempotency-Key（UUID）");
  if (typeof expectedRevision !== "number" || expectedRevision < 1) return failure(400, "invalid_input", "版本号无效");
  const fingerprint = JSON.stringify({ type: "correct", releaseId, expectedRevision, changes });
  const replay = replayOrConflict(data, key, fingerprint);
  if (replay !== null) return replay;
  const state = stateOf(data, releaseId);
  if (state === undefined) return failure(404, "not_found", "发行版本不存在");
  if (state.revision !== expectedRevision) return failure(409, "revision_conflict", "这张专辑刚被修改过，请刷新后再试");

  const next: Record<CorrectableField, Intent> = { ...state.corrections };
  const changed: CorrectableField[] = [];
  for (const [name, change] of Object.entries(changes)) {
    const field = CORRECTABLE_FIELDS.find((candidate) => candidate === name);
    if (field === undefined) return failure(400, "invalid_correction", "不能修正这个字段");
    const wanted = intentOf(change);
    if (wanted === "invalid") return failure(400, "invalid_correction", "修改方式无效");
    if (wanted?.mode === "null" && (field === "title" || field === "album_artist")) return failure(400, "invalid_correction", `${field === "title" ? "标题" : "专辑艺人"}不能留空`);
    if (!sameIntent(state.corrections[field], wanted)) {
      next[field] = wanted;
      changed.push(field);
    }
  }
  if (changed.length === 0) return failure(400, "no_changes", "没有需要修改的内容");

  const before = { ...state.corrections };
  const beforeEffective = Object.fromEntries(changed.map((field) => [field, effectiveOf(state, field)])) as Record<string, Scalar>;
  state.corrections = next;
  state.revision += 1;
  syncRelease(data, releaseId);
  syncAttention(data, releaseId);
  const toIntentDTO = (intent: Intent) => (intent === null ? null : intent.mode === "null" ? { mode: "null" as const } : { mode: "set" as const, value: intent.value });
  const record = nextOperation(
    data,
    "release_correction",
    { type: "release", id: releaseId },
    state.revision,
    {
      kind: "release_correction",
      fields: CORRECTABLE_FIELDS.filter((field) => changed.includes(field)).map((field) => ({
        field,
        before: { correction: toIntentDTO(before[field]), effective: beforeEffective[field] ?? null },
        after: { correction: toIntentDTO(next[field]), effective: effectiveOf(state, field) },
      })),
    },
    () => {
      for (const field of changed) state.corrections[field] = before[field];
    },
  );
  return remember(data, key, fingerprint, { status: 200, body: { ...stateDTO(releaseId, state), operation_id: record.id } });
}

// ---- 人工归组 ----

type RawGroupBody = { expected_revision?: unknown; action?: unknown; group_id?: unknown };

const titleOfGroup = (catalog: CatalogData, groupId: string): string => groupDetailFor(catalog, groupId)?.representative.title ?? "";

/** 把发行移到另一个组（"new" 表示新建一个组）；返回原来与现在的组。 */
function moveRelease(data: GovernanceData, releaseId: string, target: string): { from: string; to: string } {
  const { catalog } = data;
  const from = groupIdOf(catalog, releaseId) as string;
  const summary = membersOf(catalog, from).find((member) => member.id === releaseId) as ReleaseSummaryDTO;
  const to = target === "new" ? uuidOf("a", 900 + data.counter) : target;
  writeGroup(data, from, membersOf(catalog, from).filter((member) => member.id !== releaseId));
  writeGroup(data, to, [...membersOf(catalog, to), summary]);
  sortGroups(catalog);
  return { from, to };
}

export function respondToGroupChange(data: GovernanceData, releaseId: string, key: string | null, body: unknown): ApiResult {
  const { expected_revision: expectedRevision, action, group_id: groupId } = (body ?? {}) as RawGroupBody;
  if (key === null) return failure(400, "invalid_input", "请求需要 Idempotency-Key（UUID）");
  if (typeof expectedRevision !== "number" || expectedRevision < 1) return failure(400, "invalid_input", "版本号无效");
  if (action !== "join" && action !== "detach") return failure(400, "invalid_input", "归组动作无效");
  if (action === "join" && typeof groupId !== "string") return failure(400, "invalid_input", "目标专辑标识无效");
  const fingerprint = JSON.stringify({ type: "group", releaseId, expectedRevision, action, groupId: groupId ?? null });
  const replay = replayOrConflict(data, key, fingerprint);
  if (replay !== null) return replay;
  const state = stateOf(data, releaseId);
  if (state === undefined) return failure(404, "not_found", "发行版本不存在");
  if (state.revision !== expectedRevision) return failure(409, "revision_conflict", "这张专辑刚被修改过，请刷新后再试");

  const { catalog } = data;
  const current = groupIdOf(catalog, releaseId) as string;
  const others = (groupIdOfInterest: string) => membersOf(catalog, groupIdOfInterest).filter((member) => member.id !== releaseId);
  const fromTitle = titleOfGroup(catalog, current);
  let toTitle: string;
  let target: string;
  if (action === "join") {
    target = groupId as string;
    if (target === current) return failure(409, "same_group", "这个版本已经在这张专辑里");
    if (membersOf(catalog, target).length === 0) return failure(404, "group_not_found", "目标专辑不存在");
    toTitle = titleOfGroup(catalog, target);
  } else {
    if (others(current).length === 0) return failure(409, "already_alone", "这个版本已经是独立专辑");
    target = "new";
    toTitle = state.corrections.title === null ? String(state.scan.title) : String(effectiveOf(state, "title"));
  }
  const moved = moveRelease(data, releaseId, target);
  state.revision += 1;
  syncRelease(data, releaseId);
  const record = nextOperation(
    data,
    "release_group_change",
    { type: "release", id: releaseId },
    state.revision,
    { kind: "release_group_change", from: { group_id: moved.from, group_title: fromTitle }, to: { group_id: moved.to, group_title: toTitle } },
    () => {
      moveRelease(data, releaseId, moved.from);
    },
  );
  return remember(data, key, fingerprint, {
    status: 200,
    body: { release_id: releaseId, group_id: moved.to, previous_group_id: moved.from, revision: state.revision, operation_id: record.id },
  });
}

// ---- 默认版本 ----

type RawDefaultBody = { expected_revision?: unknown; release_id?: unknown };

/** 记录里默认版本的快照：当时的有效标题、年份与版本说明（版本之后改名，记录仍写当时的样子）。 */
function snapshotOf(data: GovernanceData, releaseId: string | null): DefaultVersionDTO | null {
  if (releaseId === null) return null;
  const detail = releaseDetailFor(data.catalog, releaseId);
  const summary = releaseSummaryFor(data.catalog, releaseId);
  const source = detail ?? summary;
  return { release_id: releaseId, title: source?.title ?? "", year: source?.year ?? null, edition: detail?.edition ?? null };
}

/** 专辑现在的标题：代表版本的有效标题；专辑没有可见成员时沿用记录里的名字（这里取空，由调用方兜底）。 */
const albumTitleOf = (catalog: CatalogData, groupId: string): string => groupDetailFor(catalog, groupId)?.representative.title ?? "";

/**
 * POST /release-groups/{id}/default：设定（release_id 是版本）或清除（null）专辑的默认版本。依据后端 group_default.go 的检查顺序：
 * 请求体无效 400、幂等重放、专辑不存在或没有可见版本 404、版本号过期 409 revision_conflict、版本不在这张专辑里 409 not_in_group、
 * 请求的值等于已存的值 400 no_changes；成功之后专辑的版本号加一，记录一条专辑目标的操作。
 */
export function respondToGroupDefault(data: GovernanceData, groupId: string, key: string | null, body: unknown): ApiResult {
  const raw = (body ?? {}) as RawDefaultBody;
  if (key === null) return failure(400, "invalid_input", "请求需要 Idempotency-Key（UUID）");
  if (typeof raw.expected_revision !== "number" || raw.expected_revision < 1) return failure(400, "invalid_input", "版本号无效");
  if (raw.release_id !== null && typeof raw.release_id !== "string") return failure(400, "invalid_input", "默认版本需要是版本标识（UUID），或 null（改回自动选择）");
  const target = raw.release_id;
  const fingerprint = JSON.stringify({ type: "default", groupId, expectedRevision: raw.expected_revision, target });
  const replay = replayOrConflict(data, key, fingerprint);
  if (replay !== null) return replay;
  const { catalog } = data;
  if (groupDetailFor(catalog, groupId) === undefined) return failure(404, "not_found", "发行组不存在");
  const state = groupStateOf(data, groupId);
  if (state.revision !== raw.expected_revision) return failure(409, "revision_conflict", "这张专辑刚被修改过，请刷新后再试");
  const members = membersOf(catalog, groupId);
  if (target !== null && !members.some((member) => member.id === target)) return failure(409, "not_in_group", "这个版本不在这张专辑里，或现在不可见");
  if (target === state.defaultReleaseId) return failure(400, "no_changes", "没有需要修改的内容");

  const previous = state.defaultReleaseId;
  const titleBefore = albumTitleOf(catalog, groupId);
  const apply = (next: string | null) => {
    state.defaultReleaseId = next;
    state.revision += 1;
    writeGroup(data, groupId, membersOf(catalog, groupId));
    sortGroups(catalog);
  };
  const fromSnapshot = snapshotOf(data, previous);
  apply(target);
  const record = nextOperation(
    data,
    "release_group_default",
    { type: "release_group", id: groupId },
    state.revision,
    {
      kind: "release_group_default",
      from: { group_title: titleBefore, default: fromSnapshot },
      to: { group_title: albumTitleOf(catalog, groupId), default: snapshotOf(data, target) },
    },
    () => apply(previous),
  );
  return remember(data, key, fingerprint, { status: 200, body: { group_id: groupId, default_release_id: target, revision: state.revision, operation_id: record.id } });
}

// ---- 撤销与操作记录 ----

/** 撤销记录的前后状态与它撤销的操作相反。 */
function invertChanges(changes: OperationChangesDTO): OperationChangesDTO {
  if (changes.kind === "release_group_change") return { kind: "release_group_change", from: changes.to, to: changes.from };
  if (changes.kind === "release_group_default") return { kind: "release_group_default", from: changes.to, to: changes.from };
  if (changes.kind === "release_issue_review") return { kind: "release_issue_review", issue: changes.issue, from: changes.to, to: changes.from };
  // 认领的撤销记录只带来源（没有 moved、skipped 与认领之前的组）。
  if (changes.kind === "release_claim") return { kind: "release_claim", source: changes.source };
  return { kind: "release_correction", fields: changes.fields.map((field) => ({ field: field.field, before: field.after, after: field.before })) };
}

function itemOf(data: GovernanceData, record: OperationRecord): OperationItemDTO {
  const common = { id: record.id, type: record.type, created_at: record.createdAt, actor: data.actor, changes: record.changes, reverts: record.reverts, reverted_by: record.revertedBy };
  const notReverted = record.type !== "revert" && record.revertedBy === null;
  if (record.target.type === "release_group") {
    const groupId = record.target.id;
    const visible = groupDetailFor(data.catalog, groupId) !== undefined;
    // 专辑没有可见成员时标题取记录里当时的专辑名。
    const fallback = record.changes.kind === "release_group_default" ? record.changes.to.group_title : "";
    const album = { id: groupId, title: albumTitleOf(data.catalog, groupId) || fallback, visible };
    return { ...common, release: null, album, revertible: notReverted && visible && groupStateOf(data, groupId).revision === record.resultRevision };
  }
  const releaseId = record.target.id;
  const detail = releaseDetailFor(data.catalog, releaseId);
  const groupId = groupIdOf(data.catalog, releaseId);
  const state = data.releases.get(releaseId);
  const release = detail === undefined || groupId === undefined ? null : { id: releaseId, title: detail.title, group_id: groupId, visible: true };
  return { ...common, release, album: null, revertible: notReverted && release !== null && state !== undefined && state.revision === record.resultRevision };
}

export function respondToOperations(data: GovernanceData, query: URLSearchParams): ApiResult {
  const page = Number(query.get("page") ?? "1");
  const pageSize = Number(query.get("page_size") ?? "50");
  if (!Number.isInteger(page) || page < 1 || !Number.isInteger(pageSize) || pageSize < 1 || pageSize > 100) return failure(400, "invalid_pagination", "分页参数无效");
  const releaseId = query.get("release_id");
  const groupId = query.get("group_id");
  // 两个筛选都给出时取并集：这个版本自己的记录，加上这张专辑的记录。
  const matching = data.operations
    .filter((record) => (releaseId === null && groupId === null) || (releaseId !== null && record.target.type === "release" && record.target.id === releaseId) || (groupId !== null && record.target.type === "release_group" && record.target.id === groupId))
    .reverse();
  const start = (page - 1) * pageSize;
  return {
    status: 200,
    body: { items: matching.slice(start, start + pageSize).map((record) => itemOf(data, record)), pagination: { page, page_size: pageSize, total: matching.length } },
  };
}

export function respondToRevert(data: GovernanceData, operationId: string, key: string | null): ApiResult {
  if (key === null) return failure(400, "invalid_input", "请求需要 Idempotency-Key（UUID）");
  const fingerprint = JSON.stringify({ type: "revert", operationId });
  const replay = replayOrConflict(data, key, fingerprint);
  if (replay !== null) return replay;
  const original = data.operations.find((record) => record.id === operationId);
  if (original === undefined) return failure(404, "not_found", "操作记录不存在");
  if (original.type === "revert") return failure(409, "not_revertible", "撤销不能再被撤销；需要的话请重新修改");
  if (original.revertedBy !== null) return failure(409, "already_reverted", "这条操作已经撤销过了");
  // 目标的版本号：发行与专辑各有自己的。撤销之后目标的版本号加一，只有最近一次修改可以撤销。
  const targetState = original.target.type === "release_group" ? (groupDetailFor(data.catalog, original.target.id) === undefined ? undefined : groupStateOf(data, original.target.id)) : stateOf(data, original.target.id);
  if (targetState === undefined) return failure(404, "not_found", original.target.type === "release_group" ? "专辑不存在" : "发行版本不存在");
  if (targetState.revision !== original.resultRevision) return failure(409, "revert_conflict", "这张专辑之后又被修改过，不能撤销这一条");

  original.restore();
  if (original.target.type === "release") {
    targetState.revision += 1;
    syncRelease(data, original.target.id);
    syncAttention(data, original.target.id);
  }
  // 专辑目标的撤销（restore 里已经递增了专辑的版本号并重写了组）。
  const revert = nextOperation(data, "revert", original.target, targetState.revision, invertChanges(original.changes), () => undefined, original.id);
  original.revertedBy = revert.id;
  return remember(data, key, fingerprint, {
    status: 200,
    body: { operation_id: revert.id, reverts_operation_id: original.id, target_type: original.target.type, target_id: original.target.id, revision: targetState.revision },
  });
}

// ---- 元数据问题与核对 ----

/** 依据令牌：问题内容的稳定摘要（64 位十六进制，形状与后端相同）。内容不变令牌不变。 */
function tokenOf(content: unknown): string {
  const text = JSON.stringify(content);
  let hash = 0x811c9dc5;
  for (let index = 0; index < text.length; index += 1) hash = Math.imul(hash ^ text.charCodeAt(index), 0x01000193) >>> 0;
  return hash.toString(16).padStart(8, "0").repeat(8);
}

const fieldOf = (seed: IssueSeed, field: string) => seed.fields?.find((candidate) => candidate.field === field);

/** 一类问题现在的依据令牌；问题现在不存在时为 null。 */
function issueBasis(seed: IssueSeed, issue: ReviewableIssue): string | null {
  if (issue === "grouping") return seed.grouping == null ? null : tokenOf(seed.grouping.sourceRefs);
  if (issue === "missing_tracks") return seed.missing === undefined || seed.missing.length === 0 ? null : tokenOf(seed.missing.map((track) => [track.id, track.lastSeenAt]));
  const field = fieldOf(seed, issue);
  return field === undefined ? null : tokenOf([field.reason, field.value, field.candidates]);
}

function reviewValid(state: IssueState, issue: ReviewableIssue): boolean {
  const review = state.reviews.get(issue);
  return review !== undefined && review.basis === issueBasis(state.seed, issue);
}

/** 缺失曲目逐首判断：这首在核对的依据里，并且最后一次看到的时间没变。 */
function trackReviewed(state: IssueState, track: { id: string; lastSeenAt: string }): boolean {
  const review = state.reviews.get("missing_tracks") as MissingReview | undefined;
  return review !== undefined && review.tracks.get(track.id) === track.lastSeenAt;
}

/** 字段是否还算问题：有人工修正的字段不算（专辑艺人的修正覆盖展示艺人）。 */
function fieldStillOpen(data: GovernanceData, releaseId: string, field: string): boolean {
  const correction = data.releases.get(releaseId)?.corrections;
  if (correction === undefined) return true;
  const key = field === "artist" ? "album_artist" : field;
  return !CORRECTABLE_FIELDS.some((name) => name === key && correction[name] !== null);
}

function countsOf(data: GovernanceData, releaseId: string, state: IssueState): { open: IssueCountsDTO; reviewed: IssueCountsDTO } {
  const fields = (state.seed.fields ?? []).filter((field) => fieldStillOpen(data, releaseId, field.field));
  const reviewedField = (field: string) => (field === "source_type" || field === "media_type") && reviewValid(state, field);
  const missing = state.seed.missing ?? [];
  const groupingReviewed = state.seed.grouping != null && reviewValid(state, "grouping");
  return {
    open: {
      fields: fields.filter((field) => !reviewedField(field.field)).length,
      grouping: state.seed.grouping != null && !groupingReviewed ? 1 : 0,
      missing_tracks: missing.filter((track) => !trackReviewed(state, track)).length,
    },
    reviewed: {
      fields: fields.filter((field) => reviewedField(field.field)).length,
      grouping: groupingReviewed ? 1 : 0,
      missing_tracks: missing.filter((track) => trackReviewed(state, track)).length,
    },
  };
}

const sumOf = (counts: IssueCountsDTO) => counts.fields + counts.grouping + counts.missing_tracks;

/** 把版本的“需要关注”写回专辑数据的各处（列表、组详情的成员与合计、版本详情），与后端用同一个算法得到的数一致。 */
function syncAttention(data: GovernanceData, releaseId: string): void {
  const state = data.issues.get(releaseId);
  const groupId = groupIdOf(data.catalog, releaseId);
  if (state === undefined || groupId === undefined) return;
  const attention = sumOf(countsOf(data, releaseId, state).open);
  writeGroup(data, groupId, membersOf(data.catalog, groupId).map((member) => (member.id === releaseId ? { ...member, attention_count: attention } : member)));
  const detail = data.catalog.releases.get(releaseId) ?? releaseDetailFor(data.catalog, releaseId);
  if (detail !== undefined) data.catalog.releases.set(releaseId, { ...detail, attention_count: attention });
}

/** 测试预置：给版本登记问题（扫描结论），“需要关注”随之变为待处理数目之和。 */
export function seedIssues(data: GovernanceData, releaseId: string, seed: IssueSeed): void {
  if (releaseSummaryFor(data.catalog, releaseId) === undefined) throw new Error(`没有发行 ${releaseId}`);
  data.issues.set(releaseId, { seed, reviews: new Map() });
  syncAttention(data, releaseId);
}

/** 模拟一次重新扫描：版本的问题变成新的样子（核对行保留，依据变了的问题重新计入）。 */
export function scanIssues(data: GovernanceData, releaseId: string, seed: IssueSeed): void {
  const state = data.issues.get(releaseId);
  if (state === undefined) throw new Error(`发行 ${releaseId} 没有登记问题`);
  state.seed = seed;
  syncAttention(data, releaseId);
}

function issueDetailOf(data: GovernanceData, releaseId: string, state: IssueState): ReleaseIssuesDTO {
  const summary = releaseSummaryFor(data.catalog, releaseId) as ReleaseSummaryDTO;
  const detail = releaseDetailFor(data.catalog, releaseId);
  const groupId = groupIdOf(data.catalog, releaseId) as string;
  const reviewOf = (issue: ReviewableIssue) => {
    const review = state.reviews.get(issue);
    return review === undefined ? null : { operation_id: review.operationId, reviewed_at: review.reviewedAt, reviewer: data.actor };
  };
  const fields = (state.seed.fields ?? [])
    .filter((field) => fieldStillOpen(data, releaseId, field.field))
    .map((field): FieldIssueDTO => {
      const reviewable = field.field === "source_type" || field.field === "media_type";
      const issue = field.field as ReviewableIssue;
      return {
        field: field.field,
        value: field.value,
        reason_code: field.reason,
        candidates: [...field.candidates],
        reviewable,
        basis: reviewable ? issueBasis(state.seed, issue) : null,
        status: reviewable && reviewValid(state, issue) ? "reviewed" : "open",
        review: reviewable ? reviewOf(issue) : null,
      };
    })
    .sort((a, b) => a.field.localeCompare(b.field));
  const { grouping } = state.seed;
  const missing = state.seed.missing ?? [];
  const counts = countsOf(data, releaseId, state);
  return {
    release_id: releaseId,
    group_id: groupId,
    revision: stateOf(data, releaseId)?.revision ?? 1,
    release: { ...summary, attention_count: sumOf(counts.open) },
    edition: detail?.edition ?? null,
    fields,
    grouping:
      grouping == null
        ? null
        : {
            reason_codes: ["same_directory_conflict"],
            source_refs: [...grouping.sourceRefs],
            truncated: false,
            siblings: (grouping.siblings ?? []).map((sibling) => ({ release_id: sibling.releaseId, group_id: sibling.groupId, title: sibling.title })),
            basis: issueBasis(state.seed, "grouping") as string,
            status: reviewValid(state, "grouping") ? "reviewed" : "open",
            review: reviewOf("grouping"),
          },
    missing_tracks:
      missing.length === 0
        ? null
        : {
            count: missing.length,
            tracks: missing.map((track) => ({
              id: track.id,
              title: track.title,
              medium_position: track.medium,
              position: track.position,
              source: track.source,
              last_seen_at: track.lastSeenAt,
              reviewed: trackReviewed(state, track),
            })),
            truncated: false,
            basis: issueBasis(state.seed, "missing_tracks") as string,
            status: counts.open.missing_tracks > 0 ? "open" : "reviewed",
            review: reviewOf("missing_tracks"),
          },
  };
}

/** GET /releases/{id}/issues：版本不可见 404；没有登记问题的版本返回空的问题。 */
export function respondToReleaseIssues(data: GovernanceData, releaseId: string): ApiResult {
  if (releaseDetailFor(data.catalog, releaseId) === undefined || groupIdOf(data.catalog, releaseId) === undefined) return failure(404, "not_found", "发行版本不存在");
  const state: IssueState = data.issues.get(releaseId) ?? { seed: {}, reviews: new Map() };
  return { status: 200, body: issueDetailOf(data, releaseId, state) };
}

const ISSUE_PAGE_MAX = 100;

/** GET /release-issues：状态、类别与分页；按有效标题、艺人与 ID 排序；summary 是筛选项上的数字。 */
export function respondToIssues(data: GovernanceData, query: URLSearchParams): ApiResult {
  const page = Number(query.get("page") ?? "1");
  const pageSize = Number(query.get("page_size") ?? "50");
  if (!Number.isInteger(page) || page < 1 || !Number.isInteger(pageSize) || pageSize < 1 || pageSize > ISSUE_PAGE_MAX) return failure(400, "invalid_pagination", "分页参数无效");
  const status = query.get("status") ?? "open";
  const category = query.get("category");
  if ((status !== "open" && status !== "reviewed") || (category !== null && category !== "fields" && category !== "grouping" && category !== "missing_tracks")) {
    return failure(400, "invalid_filter", "问题筛选条件无效");
  }
  const rows: IssueListItemDTO[] = [];
  for (const [releaseId, state] of data.issues) {
    const summary = releaseSummaryFor(data.catalog, releaseId);
    const groupId = groupIdOf(data.catalog, releaseId);
    if (summary === undefined || groupId === undefined || releaseDetailFor(data.catalog, releaseId) === undefined) continue;
    const counts = countsOf(data, releaseId, state);
    rows.push({ release: { ...summary, attention_count: sumOf(counts.open) }, group_id: groupId, edition: releaseDetailFor(data.catalog, releaseId)?.edition ?? null, ...counts });
  }
  rows.sort(
    (a, b) => lower(a.release.title).localeCompare(lower(b.release.title)) || lower(a.release.artist).localeCompare(lower(b.release.artist)) || a.release.id.localeCompare(b.release.id),
  );
  const has = (counts: IssueCountsDTO, which: string | null) => (which === null ? sumOf(counts) > 0 : counts[which as keyof IssueCountsDTO] > 0);
  const summaryOf = (side: "open" | "reviewed") => ({
    all: rows.filter((row) => has(row[side], null)).length,
    fields: rows.filter((row) => has(row[side], "fields")).length,
    grouping: rows.filter((row) => has(row[side], "grouping")).length,
    missing_tracks: rows.filter((row) => has(row[side], "missing_tracks")).length,
  });
  const matching = rows.filter((row) => has(row[status], category));
  const start = (page - 1) * pageSize;
  return {
    status: 200,
    body: {
      items: matching.slice(start, start + pageSize),
      summary: { open: summaryOf("open"), reviewed: summaryOf("reviewed") },
      pagination: { page, page_size: pageSize, total: matching.length },
    },
  };
}

type RawReviewBody = { expected_revision?: unknown; issue?: unknown; action?: unknown; basis?: unknown };

/**
 * POST /releases/{id}/issue-review：依据后端 issue_review.go 的检查顺序：请求体无效 400、幂等重放、版本不可见 404、版本号过期 409 revision_conflict；
 * review 时问题不存在或依据令牌不同 409 issue_changed，已经核对过且仍有效 400 no_changes；unreview 时没有核对 400 no_changes。
 * 成功后发行的版本号加一，记录一条“核对问题”；撤销把核对恢复成前态。
 */
export function respondToIssueReview(data: GovernanceData, releaseId: string, key: string | null, body: unknown): ApiResult {
  const raw = (body ?? {}) as RawReviewBody;
  if (key === null) return failure(400, "invalid_input", "请求需要 Idempotency-Key（UUID）");
  if (typeof raw.expected_revision !== "number" || raw.expected_revision < 1) return failure(400, "invalid_input", "版本号无效");
  const issue = REVIEWABLE_ISSUES.find((candidate) => candidate === raw.issue);
  if (issue === undefined) return failure(400, "invalid_input", "这类问题不能标记为已核对");
  if (raw.action === "review" && (typeof raw.basis !== "string" || raw.basis === "")) return failure(400, "invalid_input", "核对需要问题的依据");
  if (raw.action !== "review" && raw.action !== "unreview") return failure(400, "invalid_input", "核对动作无效");
  const fingerprint = JSON.stringify({ type: "review", releaseId, expectedRevision: raw.expected_revision, issue, action: raw.action, basis: raw.basis ?? null });
  const replay = replayOrConflict(data, key, fingerprint);
  if (replay !== null) return replay;
  const release = stateOf(data, releaseId);
  if (release === undefined || groupIdOf(data.catalog, releaseId) === undefined) return failure(404, "not_found", "发行版本不存在");
  if (release.revision !== raw.expected_revision) return failure(409, "revision_conflict", "这张专辑刚被修改过，请刷新后再试");
  const state: IssueState = data.issues.get(releaseId) ?? { seed: {}, reviews: new Map() };
  data.issues.set(releaseId, state);
  const previous = state.reviews.get(issue);
  let next: IssueReview | MissingReview | undefined;
  if (raw.action === "review") {
    const basis = issueBasis(state.seed, issue);
    if (basis === null || basis !== raw.basis) return failure(409, "issue_changed", "扫描刚刚改变了这个问题，请重新读取后再核对");
    const open = countsOf(data, releaseId, state).open;
    const stillOpen = issue === "grouping" ? open.grouping > 0 : issue === "missing_tracks" ? open.missing_tracks > 0 : !reviewValid(state, issue);
    if (!stillOpen) return failure(400, "no_changes", "这个问题已经核对过了");
    next = { basis, operationId: "", reviewedAt: "" };
    if (issue === "missing_tracks") next = { ...next, tracks: new Map((state.seed.missing ?? []).map((track) => [track.id, track.lastSeenAt])) };
  } else if (previous === undefined) {
    return failure(400, "no_changes", "这个问题没有核对过");
  }
  const trackCount = (review: IssueReview | MissingReview | undefined) => (review !== undefined && "tracks" in review ? review.tracks.size : null);
  release.revision += 1;
  const record = nextOperation(
    data,
    "release_issue_review",
    { type: "release", id: releaseId },
    release.revision,
    { kind: "release_issue_review", issue, from: { reviewed: previous !== undefined, track_count: trackCount(previous) }, to: { reviewed: next !== undefined, track_count: trackCount(next) } },
    () => {
      if (previous === undefined) state.reviews.delete(issue);
      else state.reviews.set(issue, previous);
      syncAttention(data, releaseId);
    },
  );
  if (next === undefined) state.reviews.delete(issue);
  else state.reviews.set(issue, { ...next, operationId: record.id, reviewedAt: record.createdAt });
  syncRelease(data, releaseId);
  syncAttention(data, releaseId);
  return remember(data, key, fingerprint, {
    status: 200,
    body: { release_id: releaseId, issue, reviewed: next !== undefined, revision: release.revision, operation_id: record.id },
  });
}

// ---- 待认领（M4 第三项）----

/**
 * 测试预置：登记一个待认领的旧版本；detail 就是 GET /release-claims/{id} 的响应体。effects 是旧版本上迁移的核对在新版本上的效果
 * （valid：新版本有同样的问题、依据相同；stale：依据不同；默认 no_issue：新版本没有这类问题）。
 */
export function seedClaim(data: GovernanceData, detail: ClaimDetailDTO, effects: Partial<Record<ReviewableIssue, ClaimEffect>> = {}): void {
  data.claims.set(detail.release.id, detail);
  data.claimEffects.set(detail.release.id, effects);
}

const CLAIM_PAGE_MAX = 100;

/** 详情里列表项的部分：release、album、location、last_seen_at 与 holds。 */
function claimEntryOf(detail: ClaimDetailDTO): ClaimEntryDTO {
  return { release: detail.release, album: detail.album, location: detail.location, last_seen_at: detail.last_seen_at, holds: detail.holds };
}

/** GET /release-claims：按最后一次被看到的时间倒序（再按 ID）、分页，参数无效 400 invalid_pagination。 */
export function respondToClaims(data: GovernanceData, query: URLSearchParams): ApiResult {
  const page = Number(query.get("page") ?? "1");
  const pageSize = Number(query.get("page_size") ?? "50");
  if (!Number.isInteger(page) || page < 1 || !Number.isInteger(pageSize) || pageSize < 1 || pageSize > CLAIM_PAGE_MAX) return failure(400, "invalid_pagination", "分页参数无效");
  const rows = [...data.claims.values()].sort((a, b) => Date.parse(b.last_seen_at) - Date.parse(a.last_seen_at) || a.release.id.localeCompare(b.release.id));
  const start = (page - 1) * pageSize;
  return { status: 200, body: { items: rows.slice(start, start + pageSize).map(claimEntryOf), pagination: { page, page_size: pageSize, total: rows.length } } };
}

/** GET /release-claims/{id}：不满足待认领的判定（没有登记，或认领之后）404。 */
export function respondToClaim(data: GovernanceData, releaseId: string): ApiResult {
  const detail = data.claims.get(releaseId);
  return detail === undefined ? failure(404, "not_found", "发行版本不存在") : { status: 200, body: detail };
}

const CLAIM_HINTS_MAX = 3;

/** GET /releases/{id}/claim-hints：把这个可见版本列为推荐的待认领旧版本（最近看到的在前，最多 3 个）；版本不可见 404。 */
export function respondToClaimHints(data: GovernanceData, releaseId: string): ApiResult {
  if (releaseDetailFor(data.catalog, releaseId) === undefined) return failure(404, "not_found", "发行版本不存在");
  const items = [...data.claims.values()]
    .filter((detail) => detail.suggestions.some((suggestion) => suggestion.release.id === releaseId))
    .sort((a, b) => Date.parse(b.last_seen_at) - Date.parse(a.last_seen_at) || a.release.id.localeCompare(b.release.id))
    .slice(0, CLAIM_HINTS_MAX)
    .map((detail) => ({ id: detail.release.id, title: detail.release.title }));
  return { status: 200, body: { items } };
}

// ---- 认领的预览与写入 ----

/** 后端认领的核对类别顺序（claimReviewOrder）；前端 REVIEWABLE_ISSUES 的顺序不同，预览按后端的来。 */
const CLAIM_REVIEW_ORDER: readonly ReviewableIssue[] = ["source_type", "media_type", "grouping", "missing_tracks"];

/** 目标现在的修正意图（与旧版本的 correction 同形状）。 */
function intentDTO(intent: Intent): { mode: "set"; value: string | number } | { mode: "null" } | undefined {
  if (intent === null) return undefined;
  return intent.mode === "null" ? { mode: "null" } : { mode: "set", value: intent.value };
}

type ClaimPlan = { items: ClaimItemDTO[]; moveAlbum: boolean; leaveAlbum: boolean; finalGroup: string };

/** 与后端 buildClaimPlan 同序同规则：修正、专辑、默认版本、核对。 */
function claimPlanOf(data: GovernanceData, detail: ClaimDetailDTO, targetId: string): ClaimPlan {
  const target = stateOf(data, targetId) as ReleaseState;
  const targetGroup = groupIdOf(data.catalog, targetId) as string;
  const effects = data.claimEffects.get(detail.release.id) ?? {};
  const targetReviews = data.issues.get(targetId)?.reviews;
  const items: ClaimItemDTO[] = [];
  for (const correction of detail.corrections) {
    const current = intentDTO(target.corrections[correction.field]);
    items.push(
      current === undefined
        ? { kind: "correction", field: correction.field, action: "move", from: correction.correction }
        : { kind: "correction", field: correction.field, action: "skip", reason: "target_has_correction", from: correction.correction, to: current },
    );
  }
  let moveAlbum = false;
  let finalGroup = targetGroup;
  const leaveAlbum = detail.album !== null;
  if (detail.album !== null) {
    const group = detail.album.id;
    if (targetGroup === group) items.push({ kind: "album", group, action: "skip", reason: "already_in_album" });
    else if (membersOf(data.catalog, targetGroup).length > 1) items.push({ kind: "album", group, action: "skip", reason: "target_in_album" });
    else {
      items.push({ kind: "album", group, action: "move" });
      moveAlbum = true;
      finalGroup = group;
    }
  }
  for (const album of [...detail.default_of].sort((a, b) => a.id.localeCompare(b.id))) {
    items.push({ kind: "default", group: album.id, action: finalGroup === album.id ? "move" : "reset" });
  }
  for (const issue of CLAIM_REVIEW_ORDER) {
    if (!detail.reviews.some((review) => review.issue === issue)) continue;
    if (issue === "missing_tracks") items.push({ kind: "review", issue, action: "skip", reason: "not_transferable" });
    else if (targetReviews?.has(issue) === true) items.push({ kind: "review", issue, action: "skip", reason: "target_has_review" });
    else items.push({ kind: "review", issue, action: "move", effect: effects[issue] ?? "no_issue" });
  }
  return { items, moveAlbum, leaveAlbum, finalGroup };
}

/** 令牌只取决于来源、目标与各项的去向（不含展示用的效果）：与后端一样，状态变了令牌就变。 */
const claimTokenOf = (sourceId: string, targetId: string, items: readonly ClaimItemDTO[]): string =>
  tokenOf({ sourceId, targetId, items: items.map((item) => [item.kind, item.field ?? null, item.issue ?? null, item.group ?? null, item.action, item.reason ?? null]) });

/** 预览与写入共用的检查：新版本不可见 404；旧版本不在待认领里 409 not_claimable。 */
function claimContext(data: GovernanceData, sourceId: string, targetId: string): { detail: ClaimDetailDTO; target: ReleaseState } | ApiResult {
  const target = stateOf(data, targetId);
  if (target === undefined || groupIdOf(data.catalog, targetId) === undefined) return failure(404, "not_found", "发行版本不存在");
  const detail = data.claims.get(sourceId);
  if (detail === undefined) return failure(409, "not_claimable", "这个版本现在不需要认领：它可见，或已经没有人工修改");
  return { detail, target };
}

const isFailure = (value: { detail: ClaimDetailDTO; target: ReleaseState } | ApiResult): value is ApiResult => "status" in value;

/** GET /releases/{id}/claim-preview?source=：逐项说明旧版本上的修改会怎么处理，带两个版本的版本号和令牌。 */
export function respondToClaimPreview(data: GovernanceData, targetId: string, query: URLSearchParams): ApiResult {
  const sources = query.getAll("source");
  if (sources.length !== 1 || !/^[0-9a-f-]{36}$/i.test(sources[0] ?? "")) return failure(400, "invalid_id", "旧版本标识无效");
  const sourceId = (sources[0] as string).toLowerCase();
  if (sourceId === targetId) return failure(400, "invalid_input", "不能把一个版本认领给它自己");
  const context = claimContext(data, sourceId, targetId);
  if (isFailure(context)) return context;
  const plan = claimPlanOf(data, context.detail, targetId);
  const body: ClaimPreviewDTO = {
    release: { id: targetId, title: String(effectiveOf(context.target, "title")), group_id: groupIdOf(data.catalog, targetId) as string, visible: true },
    source: { id: sourceId, title: context.detail.release.title, group_id: context.detail.album?.id ?? uuidOf("a", 800), visible: false },
    revision: context.target.revision,
    source_revision: context.detail.revision,
    items: plan.items,
    token: claimTokenOf(sourceId, targetId, plan.items),
  };
  return { status: 200, body };
}

type RawClaimBody = { expected_revision?: unknown; source_release_id?: unknown; source_revision?: unknown; preview?: unknown };

/** 把迁移的字段、保留的字段与各类的去向整理成响应和记录里的摘要。 */
function summarizeClaimPlan(plan: ClaimPlan) {
  const pick = (action: string) => ({
    corrections: plan.items.filter((item) => item.kind === "correction" && (action === "move" ? item.action === "move" : item.action === "skip")).map((item) => item.field as CorrectableField),
    album: plan.items.some((item) => item.kind === "album" && (action === "move" ? item.action === "move" : item.action === "skip")),
    reviews: plan.items.filter((item) => item.kind === "review" && (action === "move" ? item.action === "move" : item.action === "skip")).map((item) => item.issue as ReviewableIssue),
  });
  const defaults = plan.items.filter((item) => item.kind === "default");
  return {
    moved: pick("move"),
    skipped: pick("skip"),
    defaults: { moved: defaults.filter((item) => item.action === "move").map((item) => item.group as string), reset: defaults.filter((item) => item.action === "reset").map((item) => item.group as string) },
  };
}

/**
 * POST /releases/{id}/claim：依据后端 claim.go 的检查顺序：请求体无效 400、幂等重放、新版本不可见 404、旧版本不需要认领 409 not_claimable、
 * 任一版本号不等 409 revision_conflict、预览令牌与现在的不同 409 claim_changed；成功后按预览迁移，两个版本的版本号各加一，旧版本离开待认领，
 * 记录一条“认领”，撤销把一切还原（包括把旧版本放回待认领）。
 */
export function respondToClaimRelease(data: GovernanceData, targetId: string, key: string | null, body: unknown): ApiResult {
  const raw = (body ?? {}) as RawClaimBody;
  if (key === null) return failure(400, "invalid_input", "请求需要 Idempotency-Key（UUID）");
  if (typeof raw.expected_revision !== "number" || raw.expected_revision < 1 || typeof raw.source_revision !== "number" || raw.source_revision < 1) return failure(400, "invalid_input", "版本号无效");
  if (typeof raw.source_release_id !== "string" || !/^[0-9a-f-]{36}$/i.test(raw.source_release_id)) return failure(400, "invalid_input", "旧版本标识无效");
  const sourceId = raw.source_release_id.toLowerCase();
  if (sourceId === targetId) return failure(400, "invalid_input", "不能把一个版本认领给它自己");
  if (typeof raw.preview !== "string" || raw.preview === "") return failure(400, "invalid_input", "认领需要预览的令牌");
  const fingerprint = JSON.stringify({ type: "claim", targetId, sourceId, expectedRevision: raw.expected_revision, sourceRevision: raw.source_revision, preview: raw.preview });
  const replay = replayOrConflict(data, key, fingerprint);
  if (replay !== null) return replay;
  const context = claimContext(data, sourceId, targetId);
  if (isFailure(context)) return context;
  const { detail, target } = context;
  if (target.revision !== raw.expected_revision || detail.revision !== raw.source_revision) return failure(409, "revision_conflict", "这张专辑刚被修改过，请刷新后再试");
  const plan = claimPlanOf(data, detail, targetId);
  if (claimTokenOf(sourceId, targetId, plan.items) !== raw.preview) return failure(409, "claim_changed", "两个版本的状态在预览之后变了，请重新查看预览");

  // 拍下执行前的状态：撤销要把它们原样还回去。
  const before = {
    corrections: { ...target.corrections },
    group: groupIdOf(data.catalog, targetId) as string,
    reviews: new Map(data.issues.get(targetId)?.reviews ?? []),
    hadIssueState: data.issues.has(targetId),
    groups: new Map([...detail.default_of.map((album) => album.id), ...(plan.moveAlbum ? [plan.finalGroup] : [])].map((id) => [id, { ...groupStateOf(data, id) }])),
    effects: data.claimEffects.get(sourceId) ?? {},
  };
  const summary = summarizeClaimPlan(plan);
  const sourceTitle = detail.release.title;

  for (const item of plan.items) {
    if (item.kind === "correction" && item.action === "move" && item.field !== undefined) {
      const correction = detail.corrections.find((candidate) => candidate.field === item.field);
      if (correction !== undefined) target.corrections[item.field] = correction.correction.mode === "null" ? { mode: "null" } : { mode: "set", value: correction.correction.value as string | number };
    }
  }
  if (plan.moveAlbum) moveRelease(data, targetId, plan.finalGroup);
  for (const item of plan.items) {
    if (item.kind !== "default" || item.group === undefined) continue;
    const group = groupStateOf(data, item.group);
    group.defaultReleaseId = item.action === "move" ? targetId : null;
    group.revision += 1;
  }
  const issueState = data.issues.get(targetId) ?? { seed: {}, reviews: new Map() };
  data.issues.set(targetId, issueState);
  // 核对行引用这次认领的记录：nextOperation 会把计数加一后取同一个编号。
  const claimOperationId = uuidOf("f", data.counter + 1);
  for (const item of plan.items) {
    if (item.kind !== "review" || item.action !== "move" || item.issue === undefined) continue;
    const original = detail.reviews.find((review) => review.issue === item.issue);
    // 效果为 valid 时依据与新版本现在的相同；其余（stale、no_issue）依据不同，核对不生效。
    const basis = item.effect === "valid" ? (issueBasis(issueState.seed, item.issue) ?? "migrated") : "migrated";
    issueState.reviews.set(item.issue, { basis, operationId: claimOperationId, reviewedAt: original?.reviewed_at ?? "2026-01-01T00:00:00Z" });
  }
  target.revision += 1;
  data.claims.delete(sourceId);
  // 再同步一次：组的成员（含新版本的归属）、标题与需要关注的合计。
  syncRelease(data, targetId);
  syncAttention(data, targetId);
  for (const id of before.groups.keys()) {
    const members = membersOf(data.catalog, id);
    if (members.length > 0) writeGroup(data, id, members);
  }
  sortGroups(data.catalog);

  const record = nextOperation(
    data,
    "release_claim",
    { type: "release", id: targetId },
    target.revision,
    { kind: "release_claim", source: { id: sourceId, title: sourceTitle, visible: false }, previous_group_id: before.group, moved: { ...summary.moved, defaults: summary.defaults.moved.length }, skipped: { ...summary.skipped, defaults: 0 } },
    () => {
      target.corrections = { ...before.corrections };
      if (plan.moveAlbum) moveRelease(data, targetId, before.group);
      for (const [id, saved] of before.groups) {
        const group = groupStateOf(data, id);
        group.defaultReleaseId = saved.defaultReleaseId;
        group.revision += 1;
      }
      const restoredIssues = data.issues.get(targetId);
      if (before.hadIssueState && restoredIssues !== undefined) restoredIssues.reviews = new Map(before.reviews);
      else data.issues.delete(targetId);
      data.claims.set(sourceId, detail);
      data.claimEffects.set(sourceId, before.effects);
      for (const id of before.groups.keys()) {
        const members = membersOf(data.catalog, id);
        if (members.length > 0) writeGroup(data, id, members);
      }
    },
  );
  return remember(data, key, fingerprint, {
    status: 200,
    body: {
      release_id: targetId,
      source_release_id: sourceId,
      group_id: groupIdOf(data.catalog, targetId) as string,
      previous_group_id: before.group,
      revision: target.revision,
      source_revision: detail.revision + 1,
      operation_id: record.id,
      moved: summary.moved,
      skipped: summary.skipped,
      defaults: summary.defaults,
    },
  });
}
