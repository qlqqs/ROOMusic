import type { ReleaseGroupSummaryDTO, ReleaseSummaryDTO } from "../features/catalog/api/decoders";
import {
  CORRECTABLE_FIELDS,
  type CorrectableField,
  type CorrectionFieldDTO,
  type CorrectionStateDTO,
  type DefaultVersionDTO,
  type OperationChangesDTO,
  type OperationItemDTO,
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

type OperationType = "release_correction" | "release_group_change" | "release_group_default" | "revert";

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

export type GovernanceData = {
  catalog: CatalogData;
  /** 记录里的操作者。 */
  actor: { id: string; username: string };
  releases: Map<string, ReleaseState>;
  groups: Map<string, GroupState>;
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
  }
  // 专辑目标的撤销（restore 里已经递增了专辑的版本号并重写了组）。
  const revert = nextOperation(data, "revert", original.target, targetState.revision, invertChanges(original.changes), () => undefined, original.id);
  original.revertedBy = revert.id;
  return remember(data, key, fingerprint, {
    status: 200,
    body: { operation_id: revert.id, reverts_operation_id: original.id, target_type: original.target.type, target_id: original.target.id, revision: targetState.revision },
  });
}
