import type { ApiFailure } from "../../../shared/api";
import type { PlanDTO, ReviewDTO } from "../api/decoders";

// 审查的界面状态（C7，应用级，按方案 ID 存）：运行里收到的审查（开始、结论），用户选了“我自己确认”，以及重新审查的请求状态。
// 与方案的 store 一样放在模块级：面板与整页互换、切页面都不丢；只在内存里，登出或换用户时由应用清掉。
// 刷新后的审查结果以会话详情里方案的 review 为准，这里只补运行中还没落库的部分。

export type ReviewEntry = {
  conversationId: string;
  planId: string;
  /** 运行里收到的审查：reviewing 为真时还在审查（收到了 review_started，还没有结论）。 */
  live: { model: string; reviewing: boolean; review: ReviewDTO | null } | null;
  /** 审查不可用后用户选了“我自己确认”：显示确认卡片，所有项都由用户决定。 */
  manual: boolean;
  /** 重新审查的请求：已发出、流还没开起来。 */
  submitting: boolean;
  /** 重新审查在开流前被拒绝的原因；再次提交时清掉。 */
  failure: ApiFailure | null;
};

let reviews: ReadonlyMap<string, ReviewEntry> = new Map();
const listeners = new Set<() => void>();
/** resetReviews 之后递增：旧用户的异步结果据此不再写入。 */
let epoch = 0;

function publish(next: ReadonlyMap<string, ReviewEntry>) {
  reviews = next;
  for (const listener of listeners) listener();
}

export function subscribeReviews(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getReviewSnapshot(): ReadonlyMap<string, ReviewEntry> {
  return reviews;
}

export function reviewEpoch(): number {
  return epoch;
}

function entryOf(conversationId: string, planId: string): ReviewEntry {
  return { conversationId, planId, live: null, manual: false, submitting: false, failure: null };
}

/** 改一份方案的审查状态；还没有记过时先按默认值记下。 */
export function updateReview(conversationId: string, planId: string, update: (entry: ReviewEntry) => Partial<ReviewEntry>): void {
  const base = reviews.get(planId) ?? entryOf(conversationId, planId);
  const next = new Map(reviews);
  next.set(planId, { ...base, ...update(base) });
  publish(next);
}

/** 运行里开始审查：之前的结论作废，显示“审查中”。 */
export function reviewStarted(conversationId: string, planId: string, model: string): void {
  updateReview(conversationId, planId, () => ({ live: { model, reviewing: true, review: null }, manual: false }));
}

/** 运行里收到审查的结论。 */
export function reviewFinished(conversationId: string, review: ReviewDTO): void {
  updateReview(conversationId, review.plan_id, () => ({ live: { model: review.model, reviewing: false, review }, manual: false }));
}

/** 这个会话的运行结束了：没有等到结论的审查不再显示“审查中”（流断开或被停止），以落库内容为准。 */
export function settleReviews(conversationId: string): void {
  let changed = false;
  const next = new Map(reviews);
  for (const [id, entry] of reviews) {
    if (entry.conversationId !== conversationId || entry.live === null || !entry.live.reviewing) continue;
    next.set(id, { ...entry, live: entry.live.review === null ? null : { ...entry.live, reviewing: false } });
    changed = true;
  }
  if (changed) publish(next);
}

/** 登出或换用户：审查的界面状态属于上一个人。 */
export function resetReviews(): void {
  epoch += 1;
  publish(new Map());
}

/** 一份方案现在要显示的审查：审查中（还没有结论）、结论，或没有审查。 */
export type ReviewView = { kind: "reviewing"; model: string } | { kind: "review"; review: ReviewDTO };

/**
 * 运行中以运行里收到的为准（会话详情还没有落库的结论）；否则以会话详情里方案的 review 为准，没有时用运行里收到的。
 */
export function reviewViewOf(entry: ReviewEntry | undefined, plan: PlanDTO | null, runActive: boolean): ReviewView | null {
  const live = entry?.live ?? null;
  if (runActive && live !== null) return live.reviewing || live.review === null ? { kind: "reviewing", model: live.model } : { kind: "review", review: live.review };
  const stored = plan?.review ?? null;
  if (stored !== null) return { kind: "review", review: stored };
  if (live?.review !== null && live?.review !== undefined) return { kind: "review", review: live.review };
  return null;
}
