import { afterEach, describe, expect, it } from "vitest";
import type { PlanDTO, ReviewDTO } from "../api/decoders";
import { getReviewSnapshot, resetReviews, reviewFinished, reviewStarted, reviewViewOf, settleReviews, updateReview } from "./review-store";

const conversationId = "00000000-0000-4000-8000-000000000001";
const planId = "00000000-0000-4000-8000-0000000000c1";
const review = (overrides: Partial<ReviewDTO> = {}): ReviewDTO => ({
  review_id: "00000000-0000-4000-8000-0000000000e1",
  plan_id: planId,
  model: "review-model",
  status: "completed",
  policy_version: 1,
  expires_at: "2026-11-16T00:15:00Z",
  items: [],
  ...overrides,
});
const plan = (stored: ReviewDTO | null): PlanDTO => ({ plan_id: planId, digest: "d", expires_at: "2026-11-17T00:00:00Z", items: [{ id: "00000000-0000-4000-8000-0000000000b1", call_id: "c1", tool: "correct_release", target: "x", changes: [], revertible: true }], review: stored });

afterEach(() => resetReviews());

describe("审查的界面状态", () => {
  it("运行中先是审查中，结论到达后是结论；开始新的审查时清掉“我自己确认”", () => {
    updateReview(conversationId, planId, () => ({ manual: true }));
    reviewStarted(conversationId, planId, "review-model");
    expect(getReviewSnapshot().get(planId)?.manual).toBe(false);
    expect(reviewViewOf(getReviewSnapshot().get(planId), plan(null), true)).toEqual({ kind: "reviewing", model: "review-model" });
    const finished = review();
    reviewFinished(conversationId, finished);
    expect(reviewViewOf(getReviewSnapshot().get(planId), plan(null), true)).toEqual({ kind: "review", review: finished });
  });

  it("运行中以运行里收到的为准；没有运行时以会话详情的为准，没有时用运行里收到的", () => {
    const stale = review({ status: "unavailable", review_id: "00000000-0000-4000-8000-0000000000e0" });
    const fresh = review();
    reviewFinished(conversationId, fresh);
    const entry = getReviewSnapshot().get(planId);
    expect(reviewViewOf(entry, plan(stale), true)).toEqual({ kind: "review", review: fresh });
    expect(reviewViewOf(entry, plan(stale), false)).toEqual({ kind: "review", review: stale });
    expect(reviewViewOf(entry, plan(null), false)).toEqual({ kind: "review", review: fresh });
    expect(reviewViewOf(undefined, plan(null), false)).toBeNull();
  });

  it("运行结束时没等到结论的审查不再是审查中；已有结论的保留", () => {
    reviewStarted(conversationId, planId, "review-model");
    settleReviews(conversationId);
    expect(getReviewSnapshot().get(planId)?.live).toBeNull();
    expect(reviewViewOf(getReviewSnapshot().get(planId), plan(null), false)).toBeNull();
    reviewFinished(conversationId, review());
    settleReviews(conversationId);
    expect(getReviewSnapshot().get(planId)?.live?.review?.review_id).toBe("00000000-0000-4000-8000-0000000000e1");
  });
});
