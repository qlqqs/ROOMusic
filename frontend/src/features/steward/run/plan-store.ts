import type { ApiFailure } from "../../../shared/api";
import type { ConversationDetailDTO, PlanDTO } from "../api/decoders";

// 修改方案的界面状态（C6，应用级）：勾选、“不再问我”、提交到哪一步、确认被拒绝的原因与每一项的撤销结果。
// 与运行一样放在模块级 store：面板与整页互换、切页面都不丢；按方案 ID 存，只在内存里，登出或换用户时由应用清掉。
// 未处理的方案以会话详情的 pending_plan 为准（刷新后据此恢复），这里只记用户对它做了什么。

/**
 * pending：等用户决定；submitting：确认请求已发出、流还没开起来；decided：已确认或“不修改”，之后显示结果；
 * superseded：用户发了新消息，方案作废；expired：过了有效期或确认时被告知已过期；gone：服务端已经没有这份方案（别处处理了）。
 */
export type PlanPhase = "pending" | "submitting" | "decided" | "superseded" | "expired" | "gone";

/** 撤销一个已执行的方案项：key 是这次撤销的幂等键，结果不确定后重试沿用它。 */
export type RevertState = { key: string } & ({ status: "pending" } | { status: "done" } | { status: "failed"; failure: ApiFailure });

export type PlanEntry = {
  conversationId: string;
  plan: PlanDTO;
  phase: PlanPhase;
  /** 勾选的方案项；默认全选。 */
  selected: ReadonlySet<string>;
  /** 勾了“这次对话里不再问我”的工具。 */
  autoApprove: ReadonlySet<string>;
  /** 提交时接受的方案项；decided 之后才有。 */
  accepted: readonly string[] | null;
  /** 确认请求在开流前被拒绝的原因（摘要不一致、对话正在回答、连不上）；再次提交时清掉。 */
  failure: ApiFailure | null;
  reverts: ReadonlyMap<string, RevertState>;
};

let plans: ReadonlyMap<string, PlanEntry> = new Map();
const listeners = new Set<() => void>();
/** resetPlans 之后递增：旧用户的异步结果据此不再写入。 */
let epoch = 0;

function publish(next: ReadonlyMap<string, PlanEntry>) {
  plans = next;
  for (const listener of listeners) listener();
}

export function subscribePlans(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getPlanSnapshot(): ReadonlyMap<string, PlanEntry> {
  return plans;
}

export function planEpoch(): number {
  return epoch;
}

function entryOf(conversationId: string, plan: PlanDTO): PlanEntry {
  return {
    conversationId,
    plan,
    phase: "pending",
    selected: new Set(plan.items.map((item) => item.id)),
    autoApprove: new Set(),
    accepted: null,
    failure: null,
    reverts: new Map(),
  };
}

/** 换成内容变了的同一份方案：原来勾着的项保持，新出现的项默认勾选，不再存在的项去掉。 */
function withPlan(entry: PlanEntry, plan: PlanDTO): PlanEntry {
  if (entry.plan.digest === plan.digest) return entry;
  const before = new Set(entry.plan.items.map((item) => item.id));
  const selected = new Set(plan.items.filter((item) => entry.selected.has(item.id) || !before.has(item.id)).map((item) => item.id));
  const tools = new Set(plan.items.map((item) => item.tool));
  return { ...entry, plan, selected, autoApprove: new Set([...entry.autoApprove].filter((tool) => tools.has(tool))) };
}

/** 运行里收到方案：没有记过就记下（等待决定）。 */
export function rememberPlan(conversationId: string, plan: PlanDTO): void {
  const current = plans.get(plan.plan_id);
  if (current !== undefined) return;
  const next = new Map(plans);
  next.set(plan.plan_id, entryOf(conversationId, plan));
  publish(next);
}

/** 改一份方案的界面状态；还没有记过（刷新后从会话详情来的）时先按默认值记下。 */
export function updatePlan(conversationId: string, plan: PlanDTO, update: (entry: PlanEntry) => Partial<PlanEntry>): void {
  const current = plans.get(plan.plan_id);
  const base = current === undefined ? entryOf(conversationId, plan) : current.phase === "pending" ? withPlan(current, plan) : current;
  const next = new Map(plans);
  next.set(plan.plan_id, { ...base, ...update(base) });
  publish(next);
}

/** 用户在有未处理方案时发了新消息：服务端把方案标为作废，各项按“未执行”回给模型。 */
export function supersedePlans(conversationId: string, detail: ConversationDetailDTO | undefined): void {
  const pending = detail?.pending_plan;
  const next = new Map(plans);
  if (pending !== undefined && pending !== null && !next.has(pending.plan_id)) next.set(pending.plan_id, entryOf(conversationId, pending));
  let changed = next.size !== plans.size;
  for (const [id, entry] of next) {
    if (entry.conversationId !== conversationId || entry.phase !== "pending") continue;
    next.set(id, { ...entry, phase: "superseded" });
    changed = true;
  }
  if (changed) publish(next);
}

/** 登出或换用户：方案的界面状态属于上一个人。 */
export function resetPlans(): void {
  epoch += 1;
  publish(new Map());
}

/**
 * 可以“这次对话里不再问我”的工具：方案里这个工具的项全部可以撤销。不可撤销的修改（艺人合并、拆开、改名、忽略候选）每次都要确认。
 */
export function autoApprovableTools(plan: PlanDTO): string[] {
  const tools = [...new Set(plan.items.map((item) => item.tool))];
  return tools.filter((tool) => plan.items.every((item) => item.tool !== tool || item.revertible));
}

/** 提交时带上的“不再问我”：勾过、可以免确认，并且这个工具至少有一项被接受。 */
export function autoApproveToolsOf(entry: PlanEntry, accepted: readonly string[]): string[] {
  return autoApprovableTools(entry.plan).filter(
    (tool) => entry.autoApprove.has(tool) && entry.plan.items.some((item) => item.tool === tool && accepted.includes(item.id)),
  );
}

/**
 * 已确认、但有接受的项还没有结果的方案（运行被停止或服务重启后）：只有“继续”会接着执行它们，发新消息时它们记为未执行。
 * 以会话详情的 plans 为准（项上没有 result 就是还没有结果）。
 */
export function unfinishedPlanIdsOf(detail: ConversationDetailDTO | undefined): ReadonlySet<string> {
  const ids = new Set<string>();
  for (const plan of detail?.plans ?? []) {
    if (plan.status !== "applied") continue;
    if (plan.items.some((item) => item.decision === "accepted" && item.result === undefined)) ids.add(plan.plan_id);
  }
  return ids;
}

/** 服务端记下的已处理方案在界面上的状态：应用或不修改都是 decided（接受的项取 decision），作废与过期照搬。 */
function entryFromServer(conversationId: string, plan: PlanDTO): PlanEntry | null {
  const entry = entryOf(conversationId, plan);
  switch (plan.status) {
    case "applied":
    case "declined":
      return { ...entry, phase: "decided", accepted: plan.items.filter((item) => item.decision === "accepted").map((item) => item.id) };
    case "superseded":
    case "expired":
      return { ...entry, phase: plan.status };
    default:
      return null;
  }
}

/**
 * 这个会话要显示的方案（按出现的先后）：本页记下的，加上会话详情里还没记过的方案（未处理的与已处理的历史）。
 * 等待决定的方案：内容以会话详情为准（摘要变了就换成新的内容）；过了有效期是 expired；
 * 会话详情已读到、本页又没有运行，服务端却已经不把它当未处理时，取服务端记下的状态，没有记录时是 gone（在别处处理了或已过期）。
 */
export function plansOf(snapshot: ReadonlyMap<string, PlanEntry>, detail: ConversationDetailDTO | undefined, conversationId: string | null, runActive: boolean, now: number): PlanEntry[] {
  if (conversationId === null) return [];
  const server = detail?.pending_plan ?? null;
  const history = new Map((detail?.plans ?? []).map((plan) => [plan.plan_id, plan]));
  const shown: PlanEntry[] = [];
  for (const entry of snapshot.values()) {
    if (entry.conversationId !== conversationId) continue;
    if (entry.phase !== "pending") {
      shown.push(entry);
      continue;
    }
    if (server !== null && server.plan_id === entry.plan.plan_id) shown.push(withPlan(entry, server));
    else if (detail !== undefined && !runActive) {
      const recorded = history.get(entry.plan.plan_id);
      const settled = recorded === undefined ? null : entryFromServer(conversationId, recorded);
      shown.push(settled === null ? { ...entry, phase: "gone" } : { ...settled, reverts: entry.reverts });
    } else shown.push(entry);
  }
  for (const plan of history.values()) {
    if (snapshot.has(plan.plan_id) || plan.plan_id === server?.plan_id) continue;
    const settled = entryFromServer(conversationId, plan);
    if (settled !== null) shown.push(settled);
  }
  if (server !== null && !snapshot.has(server.plan_id)) shown.push(entryOf(conversationId, server));
  return shown.map((entry) => (entry.phase === "pending" && Date.parse(entry.plan.expires_at) <= now ? { ...entry, phase: "expired" } : entry));
}
