import { describe, expect, it } from "vitest";
import type { ReviewEntry } from "../run/review-store";
import { sessionStateOf } from "./session-list";

const id = "00000000-0000-4000-8000-000000000001";
const item = { id, title: "清理待关注的问题", mode: "steward" as const, model: "", status: "idle" as const, created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T00:00:00Z" };
const reviewing: ReviewEntry = { conversationId: id, planId: "00000000-0000-4000-8000-0000000000c1", live: { model: "m", reviewing: true, review: null }, manual: false, submitting: false, failure: null };

describe("会话名下的状态", () => {
  it("正在回答、审查中与等你确认的项数", () => {
    const none = new Map<string, ReviewEntry>();
    expect(sessionStateOf(item, false, none)).toBeNull();
    expect(sessionStateOf({ ...item, pending_item_count: 0 }, false, none)).toBeNull();
    expect(sessionStateOf(item, true, none)).toBe("正在回答");
    expect(sessionStateOf({ ...item, pending_item_count: 1 }, false, none)).toBe("1 项等你确认");
    expect(sessionStateOf({ ...item, pending_item_count: 1 }, true, new Map([[reviewing.planId, reviewing]]))).toBe("审查中，1 项等你确认");
  });
});
