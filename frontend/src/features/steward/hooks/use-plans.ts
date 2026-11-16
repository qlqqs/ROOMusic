import { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { newIdempotencyKey } from "../../../shared/lib/ids";
import type { ConversationDetailDTO, PlanDTO } from "../api/decoders";
import { revertOperation } from "../api/endpoints";
import { useStewardHost } from "../context/host";
import { autoApproveToolsOf, getPlanSnapshot, planEpoch, plansOf, subscribePlans, updatePlan, type PlanEntry, type RevertState } from "../run/plan-store";
import { getReviewSnapshot, reviewEpoch, subscribeReviews, updateReview, type ReviewEntry } from "../run/review-store";
import type { StepItem, Turn } from "../run/reduce";
import { useStartRun } from "./use-run";

export function usePlanSnapshot(): ReadonlyMap<string, PlanEntry> {
  return useSyncExternalStore(subscribePlans, getPlanSnapshot);
}

/** 到了最早的有效期就重绘一次，等待决定的方案随即显示为已过期。 */
function useNow(deadline: number | null): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (deadline === null) return;
    // 已经过了（上次重绘之后才到的方案）也走一次定时器，立即重绘。方案的有效期是 24 小时，不超过 setTimeout 的上限。
    const timer = window.setTimeout(() => setNow(Date.now()), Math.max(deadline - Date.now(), 0) + 50);
    return () => window.clearTimeout(timer);
  }, [deadline]);
  return now;
}

/** 这个会话要显示的方案（本页记下的与会话详情里未处理的），过了有效期的标为过期。 */
export function useConversationPlans(conversationId: string | null, detail: ConversationDetailDTO | undefined, runActive: boolean): PlanEntry[] {
  const snapshot = usePlanSnapshot();
  const pending = plansOf(snapshot, detail, conversationId, runActive, Number.NEGATIVE_INFINITY).filter((entry) => entry.phase === "pending");
  const deadline = pending.length === 0 ? null : Math.min(...pending.map((entry) => Date.parse(entry.plan.expires_at)));
  const now = useNow(deadline);
  return plansOf(snapshot, detail, conversationId, runActive, now);
}

/** 方案放在哪个回合、排在第几段文字之后：方案项对应的调用所在的回合（找不到时是最后一个回合），那一步的文字之后。 */
export type PlacedPlan = { entry: PlanEntry; turnKey: string; textsBefore: number };

export function placePlans(turns: readonly Turn[], plans: readonly PlanEntry[]): PlacedPlan[] {
  const placed: PlacedPlan[] = [];
  for (const entry of plans) {
    const callIds = new Set(entry.plan.items.map((item) => item.call_id));
    let index = turns.length - 1;
    while (index >= 0 && !(turns[index]?.items.some((item) => callIds.has(item.callId)) ?? false)) index -= 1;
    const turn = turns[index >= 0 ? index : turns.length - 1];
    if (turn === undefined) continue;
    const matched = turn.items.filter((item) => callIds.has(item.callId));
    placed.push({ entry, turnKey: turn.key, textsBefore: matched.length === 0 ? turn.texts.length : Math.max(...matched.map((item) => item.textsBefore)) });
  }
  return placed;
}

/** 确认请求的拒绝里，改变方案本身状态的几种：过期、服务端已经没有这份方案。 */
function phaseAfterRefusal(failure: ReturnType<typeof failureOf>): PlanEntry["phase"] {
  if (isHttpFailure(failure, 409, "plan_expired")) return "expired";
  if (isHttpFailure(failure, 404, "plan_not_found")) return "gone";
  return "pending";
}

/**
 * 应用（accepted 是勾选的项）或“不修改”（空数组）：走确认接口的流，结果落在过程里对应的条目上，模型随后接着回答。
 * 开流前被拒绝时就地说明：过期、方案不在了改变方案的状态；摘要不一致时会话随之重读（run-store 在开流前失败时重读），
 * 卡片换成新的内容，用户看过再确认。
 */
export function useDecidePlan(conversationId: string) {
  const start = useStartRun();
  return useCallback(
    async (entry: PlanEntry, accepted: readonly string[], seed: readonly StepItem[], baseSequence: number, autoApprove: boolean) => {
      const { plan } = entry;
      const started = planEpoch();
      updatePlan(conversationId, plan, () => ({ phase: "submitting", failure: null }));
      // steward 模式的确认不接受免确认工具（后端带非空值返回 400 invalid_input），发空数组。
      const tools = autoApprove ? autoApproveToolsOf(entry, accepted) : [];
      const result = await start(conversationId, null, baseSequence, null, {
        decision: { planId: plan.plan_id, body: { digest: plan.digest, accepted_item_ids: [...accepted], auto_approve_tools: tools }, seed },
      });
      if (planEpoch() !== started) return;
      if (result.started) {
        updatePlan(conversationId, plan, () => ({ phase: "decided", accepted }));
        return;
      }
      const failure = result.failure;
      updatePlan(conversationId, plan, () => ({ phase: phaseAfterRefusal(failure), failure }));
    },
    [conversationId, start],
  );
}

export function useReviewSnapshot(): ReadonlyMap<string, ReviewEntry> {
  return useSyncExternalStore(subscribeReviews, getReviewSnapshot);
}

/**
 * 重新审查（C7，上一次审查不可用时）：走审查接口的流，审查的开始与结论、批准项的执行都在这次运行里。
 * 开流前被拒绝时就地说明；过期、方案不在了改变方案的状态。返回流是否开起来了。
 */
export function useRereviewPlan(conversationId: string) {
  const start = useStartRun();
  return useCallback(
    async (entry: PlanEntry, seed: readonly StepItem[], baseSequence: number): Promise<boolean> => {
      const { plan } = entry;
      const started = reviewEpoch();
      updateReview(conversationId, plan.plan_id, () => ({ submitting: true, failure: null }));
      const result = await start(conversationId, null, baseSequence, null, { review: { planId: plan.plan_id, seed } });
      if (reviewEpoch() !== started) return false;
      updateReview(conversationId, plan.plan_id, () => ({ submitting: false, failure: result.started ? null : result.failure }));
      if (result.started) return true;
      const phase = phaseAfterRefusal(result.failure);
      if (phase !== "pending") updatePlan(conversationId, plan, () => ({ phase }));
      return false;
    },
    [conversationId, start],
  );
}

/** 撤销一个已执行的方案项（调用受治理操作的撤销接口）；成功后让曲库的查询失效。结果不确定后重试沿用同一个幂等键。 */
export function useRevertPlanItem(conversationId: string) {
  const { refreshData } = useStewardHost();
  return useCallback(
    async (plan: PlanDTO, itemId: string, operationId: string, previousKey: string | null) => {
      const key = previousKey ?? newIdempotencyKey();
      const started = planEpoch();
      const setRevert = (state: RevertState) =>
        updatePlan(conversationId, plan, (entry) => ({ reverts: new Map(entry.reverts).set(itemId, state) }));
      setRevert({ key, status: "pending" });
      try {
        await revertOperation(operationId, key);
      } catch (error) {
        if (planEpoch() !== started) return;
        setRevert({ key, status: "failed", failure: failureOf(error) ?? { kind: "network", requestId: "" } });
        return;
      }
      if (planEpoch() !== started) return;
      setRevert({ key, status: "done" });
      refreshData("catalog");
    },
    [conversationId, refreshData],
  );
}
