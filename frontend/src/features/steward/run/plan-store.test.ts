import { afterEach, describe, expect, it } from "vitest";
import type { ConversationDetailDTO, PlanDTO } from "../api/decoders";
import {
  autoApprovableTools,
  autoApproveToolsOf,
  getPlanSnapshot,
  plansOf,
  rememberPlan,
  resetPlans,
  supersedePlans,
  unfinishedPlanIdsOf,
  updatePlan,
} from "./plan-store";

const conversationId = "00000000-0000-4000-8000-000000000001";
const item = (index: number, tool = "correct_release") => ({
  id: `00000000-0000-4000-8000-0000000000b${index}`,
  call_id: `call-${index}`,
  tool,
  target: `专辑 ${index}`,
  changes: [],
  revertible: true,
});
const plan = (overrides: Partial<PlanDTO> = {}): PlanDTO => ({
  plan_id: "00000000-0000-4000-8000-0000000000c1",
  digest: "d1",
  expires_at: "2026-11-17T00:00:00Z",
  items: [item(1), item(2, "merge_artists")],
  ...overrides,
});
const detail = (pending: PlanDTO | null, plans?: PlanDTO[]): ConversationDetailDTO => ({
  id: conversationId,
  title: "",
  mode: "assistant",
  model: "",
  status: "idle",
  created_at: "2026-11-16T00:00:00Z",
  updated_at: "2026-11-16T00:00:00Z",
  messages: [],
  pending_plan: pending,
  ...(plans === undefined ? {} : { plans }),
});
const now = Date.parse("2026-11-16T12:00:00Z");

afterEach(() => resetPlans());

describe("方案的界面状态", () => {
  it("会话详情里的未处理方案：没记过时按默认值显示（全选、没有“不再问我”）", () => {
    const [entry] = plansOf(getPlanSnapshot(), detail(plan()), conversationId, false, now);
    expect(entry?.phase).toBe("pending");
    expect([...(entry?.selected ?? [])]).toEqual([item(1).id, item(2).id]);
    expect(entry?.autoApprove.size).toBe(0);
  });

  it("过了有效期是 expired", () => {
    expect(plansOf(getPlanSnapshot(), detail(plan()), conversationId, false, Date.parse("2026-11-17T00:00:00Z"))[0]?.phase).toBe("expired");
  });

  it("摘要变了：换成新的内容，原来勾着的保持，取消勾选的保持取消，新出现的项默认勾选", () => {
    rememberPlan(conversationId, plan());
    updatePlan(conversationId, plan(), () => ({ selected: new Set([item(1).id]) }));
    const changed = plan({ digest: "d2", items: [item(1), item(2, "merge_artists"), item(3)] });
    const [entry] = plansOf(getPlanSnapshot(), detail(changed), conversationId, false, now);
    expect(entry?.plan.digest).toBe("d2");
    expect([...(entry?.selected ?? [])]).toEqual([item(1).id, item(3).id]);
  });

  it("服务端已经没有这份方案、本页又没有运行时是 gone；运行中照常等待", () => {
    rememberPlan(conversationId, plan());
    expect(plansOf(getPlanSnapshot(), detail(null), conversationId, false, now)[0]?.phase).toBe("gone");
    expect(plansOf(getPlanSnapshot(), detail(null), conversationId, true, now)[0]?.phase).toBe("pending");
  });

  it("发新消息：未处理的方案（包括只在会话详情里的）作废；已决定的不变", () => {
    const decided = plan({ plan_id: "00000000-0000-4000-8000-0000000000c2" });
    rememberPlan(conversationId, decided);
    updatePlan(conversationId, decided, () => ({ phase: "decided", accepted: [] }));
    supersedePlans(conversationId, detail(plan()));
    const phases = plansOf(getPlanSnapshot(), detail(null), conversationId, false, now).map((entry) => [entry.plan.plan_id, entry.phase]);
    expect(phases).toEqual([
      [decided.plan_id, "decided"],
      [plan().plan_id, "superseded"],
    ]);
  });

  it("别的会话的方案不显示", () => {
    rememberPlan("00000000-0000-4000-8000-000000000002", plan());
    expect(plansOf(getPlanSnapshot(), detail(null), conversationId, false, now)).toEqual([]);
  });

  it("会话详情的 plans：已处理的方案按服务端的状态显示，接受的项取 decision；本页等着的方案以服务端记下的状态为准", () => {
    const applied = plan({
      plan_id: "00000000-0000-4000-8000-0000000000c2",
      status: "applied",
      items: [{ ...item(1), decision: "accepted" }, { ...item(2, "merge_artists"), decision: "declined" }],
    });
    const expired = plan({ status: "expired" });
    rememberPlan(conversationId, plan());
    const shown = plansOf(getPlanSnapshot(), detail(null, [expired, applied]), conversationId, false, now);
    expect(shown.map((entry) => [entry.plan.plan_id, entry.phase, entry.accepted])).toEqual([
      [plan().plan_id, "expired", null],
      [applied.plan_id, "decided", [item(1).id]],
    ]);
  });

  it("“不再问我”：只给全部项都可以撤销的工具；提交时只带勾过且至少有一项被接受的", () => {
    const mixed = plan({ items: [item(1), { ...item(2, "merge_artists"), revertible: false }, item(3)] });
    expect(autoApprovableTools(mixed)).toEqual(["correct_release"]);
    rememberPlan(conversationId, mixed);
    updatePlan(conversationId, mixed, () => ({ autoApprove: new Set(["correct_release", "merge_artists"]) }));
    const entry = getPlanSnapshot().get(mixed.plan_id);
    if (entry === undefined) throw new Error("没有记下方案");
    expect(autoApproveToolsOf(entry, [item(1).id, item(2).id])).toEqual(["correct_release"]);
    expect(autoApproveToolsOf(entry, [item(2).id])).toEqual([]);
  });

  it("已确认、有接受的项没有结果的方案还没执行完", () => {
    const applied = plan({ status: "applied", items: [{ ...item(1), decision: "accepted", result: { operation_id: "o" } }, { ...item(2), decision: "accepted" }] });
    const finished = plan({ plan_id: "00000000-0000-4000-8000-0000000000c3", status: "applied", items: [{ ...item(1), decision: "accepted", result: {} }, { ...item(2), decision: "declined", result: {} }] });
    expect([...unfinishedPlanIdsOf(detail(null, [applied, finished]))]).toEqual([applied.plan_id]);
    expect(unfinishedPlanIdsOf(undefined).size).toBe(0);
  });
});
