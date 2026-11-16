import { waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { installFakeApi } from "../../../test/fake-api";
import { conversationOf, installStewardApi, planOf, planPayload, reviewOf } from "../../../test/fake-steward";
import { sampleResponse } from "../../../test/http";
import { createRunStore, getRunSnapshot, resetRuns, startRun, stopRun, type RunStore } from "./run-store";

const id = "00000000-0000-4000-8000-000000000001";

function setup() {
  const api = installFakeApi();
  const steward = installStewardApi(api, { conversations: [conversationOf(id)] });
  const refresh = vi.fn(() => Promise.resolve());
  const onToolFinished = vi.fn<(name: string) => void>();
  return { api, steward, refresh, onToolFinished, options: { context: null, refresh, onToolFinished } };
}

afterEach(() => resetRuns());

describe("进行中的运行", () => {
  it("发消息：流开起来后逐个归并事件；done 之后重读会话，再从 store 移除并留下结束信息", async () => {
    const { api, steward, refresh, options } = setup();
    const result = startRun({ conversationId: id, text: "贝九有几版？", baseSequence: 4, ...options });
    expect(getRunSnapshot().runs.get(id)).toMatchObject({ userText: "贝九有几版？", baseSequence: 4, phase: "running" });
    await expect(result).resolves.toEqual({ started: true });
    expect(api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/messages`).map((request) => request.body)).toEqual([{ text: "贝九有几版？" }]);

    const stream = steward.runs[0]?.stream;
    stream?.send("text", { type: "text", step: 1, text: "我查" });
    stream?.send("text", { type: "text", step: 1, text: "一下。" });
    stream?.send("tool_started", { type: "tool_started", step: 1, tool_started: { call_id: "c1", name: "list_albums", input: { q: "贝九" } } });
    await waitFor(() => expect(getRunSnapshot().runs.get(id)?.view.items).toHaveLength(1));
    expect(getRunSnapshot().runs.get(id)?.view.texts).toEqual(["我查一下。"]);

    stream?.send("done", { type: "done", step: 2, end: { reason: "completed" } });
    stream?.close();
    await waitFor(() => expect(getRunSnapshot().runs.has(id)).toBe(false));
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(getRunSnapshot().ends.get(id)).toEqual({ kind: "done", end: { reason: "completed" } });
  });

  it("继续运行不带请求体；同一会话已有运行时不再发起", async () => {
    const { api, options } = setup();
    await startRun({ conversationId: id, text: null, baseSequence: 2, ...options });
    await expect(startRun({ conversationId: id, text: null, baseSequence: 2, ...options })).resolves.toEqual({ started: false, failure: null });
    const requests = api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/continue`);
    expect(requests).toHaveLength(1);
    expect(requests[0]?.body).toBeUndefined();
  });

  it("页面上下文放进发消息的请求体", async () => {
    const { api, options } = setup();
    const context = { page: { path: "/me/history", kind: "other" as const, title: "播放历史" }, playback: { zone_id: id, status: "playing" } };
    await startRun({ conversationId: id, text: "这是谁录的？", baseSequence: 0, ...options, context });
    expect(api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/messages`)[0]?.body).toEqual({ text: "这是谁录的？", context });
  });

  it("工具调用结束时带上工具名通知调用方（成功与失败都算）", async () => {
    const { steward, onToolFinished, options } = setup();
    await startRun({ conversationId: id, text: "收藏这张", baseSequence: 0, ...options });
    const stream = steward.runs[0]?.stream;
    stream?.send("tool_started", { type: "tool_started", step: 1, tool_started: { call_id: "c1", name: "set_favorite", input: {} } });
    stream?.send("tool_started", { type: "tool_started", step: 1, tool_started: { call_id: "c2", name: "create_playlist", input: {} } });
    stream?.send("tool_finished", { type: "tool_finished", step: 1, tool_finished: { call_id: "c1", status: "succeeded", output: {}, truncated: false, untrusted: false } });
    stream?.send("tool_finished", { type: "tool_finished", step: 1, tool_finished: { call_id: "c2", status: "failed", output: { code: "revision_conflict" }, truncated: false, untrusted: false, error_code: "revision_conflict" } });
    await waitFor(() => expect(onToolFinished).toHaveBeenCalledTimes(2));
    expect(onToolFinished.mock.calls).toEqual([["set_favorite"], ["create_playlist"]]);
  });

  it("开始流之前被拒绝：没有运行，结束信息是拒绝的原因", async () => {
    const { steward, options } = setup();
    steward.nextRunResponse = sampleResponse("steward/conversation-busy");
    const result = await startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options });
    expect(result).toMatchObject({ started: false, failure: { kind: "http", status: 409, code: "conversation_busy" } });
    expect(getRunSnapshot().runs.has(id)).toBe(false);
    expect(getRunSnapshot().ends.get(id)).toMatchObject({ kind: "request_failed", failure: { code: "conversation_busy" } });
  });

  it("流没有 done 就断了：带着最后一个序号接回（Last-Event-ID），按序号去重后接着显示", async () => {
    const { api, steward, refresh, options } = setup();
    steward.eventLogs.add(id);
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options });
    const first = steward.runs[0]?.stream;
    first?.send("text", { type: "text", seq: 7, step: 1, text: "我查" });
    first?.send("text", { type: "text", seq: 9, step: 1, text: "一下" });
    await waitFor(() => expect(getRunSnapshot().runs.get(id)?.lastSeq).toBe(9));
    first?.fail();

    await waitFor(() => expect(steward.attaches).toHaveLength(1));
    expect(steward.attaches[0]?.after).toBe(9);
    expect(api.requestsTo("GET", `/api/v1/me/steward/conversations/${id}/events`)[0]?.headers.get("Last-Event-ID")).toBe("9");
    const again = steward.attaches[0]?.stream;
    // 已经收到过的序号丢掉；之后的文字是服务端给的准确后缀，照常追加。
    again?.send("text", { type: "text", seq: 9, step: 1, text: "一下" });
    again?.send("text", { type: "text", seq: 12, step: 1, text: "。" });
    await waitFor(() => expect(getRunSnapshot().runs.get(id)?.view.texts).toEqual(["我查一下。"]));
    expect(getRunSnapshot().runs.get(id)?.reconnecting).toBe(false);
    again?.send("done", { type: "done", seq: 13, step: 1, end: { reason: "completed" } });
    again?.close();
    await waitFor(() => expect(getRunSnapshot().ends.get(id)).toEqual({ kind: "done", end: { reason: "completed" } }));
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it("接回得到 204（服务端没有这次运行的事件）：以重读的落库内容为准，不记结束信息", async () => {
    const { steward, refresh, options } = setup();
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options });
    steward.runs[0]?.stream.fail();
    await waitFor(() => expect(getRunSnapshot().runs.has(id)).toBe(false));
    expect(getRunSnapshot().ends.has(id)).toBe(false);
    expect(refresh).toHaveBeenCalledTimes(1);
  });

  it("重新接回一直失败：按次数用完后结束信息是 disconnected", async () => {
    const { api, steward, refresh, options } = setup();
    api.on("GET", /\/events$/, () => sampleResponse("errors/database-unavailable"));
    const store = createRunStore({ reconnectDelays: [0, 0] });
    try {
      await store.startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options });
      steward.runs[0]?.stream.fail();
      await waitFor(() => expect(store.getSnapshot().ends.get(id)).toEqual({ kind: "disconnected" }));
      expect(api.requestsTo("GET", `/api/v1/me/steward/conversations/${id}/events`)).toHaveLength(2);
      expect(refresh).toHaveBeenCalledTimes(1);
    } finally {
      store.reset();
    }
  });

  it("重新接回被拒绝（4xx）：不再重试，结束信息是 disconnected", async () => {
    const { api, steward, options } = setup();
    steward.nextAttachResponse = sampleResponse("steward/disabled");
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options });
    steward.runs[0]?.stream.fail();
    await waitFor(() => expect(getRunSnapshot().ends.get(id)).toEqual({ kind: "disconnected" }));
    expect(api.requestsTo("GET", `/api/v1/me/steward/conversations/${id}/events`)).toHaveLength(1);
  });

  it("接回时较早的事件已经省略（history_gap）：通知调用方重读会话，运行标为不完整", async () => {
    const { steward, options } = setup();
    steward.eventLogs.add(id);
    const onGap = vi.fn();
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options, onGap });
    steward.runs[0]?.stream.fail();
    await waitFor(() => expect(steward.attaches).toHaveLength(1));
    steward.attaches[0]?.stream.send("history_gap", { type: "history_gap", seq: 40 });
    await waitFor(() => expect(onGap).toHaveBeenCalledTimes(1));
    expect(getRunSnapshot().runs.get(id)).toMatchObject({ lastSeq: 40, view: { gap: true } });
  });

  it("停止：调用取消接口，服务端发来 done（cancelled）后结束", async () => {
    const { api, options } = setup();
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options });
    stopRun(id);
    expect(getRunSnapshot().runs.get(id)?.phase).toBe("stopping");
    await waitFor(() => expect(getRunSnapshot().ends.get(id)).toEqual({ kind: "done", end: { reason: "cancelled" } }));
    expect(api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/cancel`)).toHaveLength(1);
  });

  it("停止时取消接口说已经没有这次运行（done 正在路上）：回到进行中，不断开连接", async () => {
    const { api, steward, options } = setup();
    api.on("POST", `/api/v1/me/steward/conversations/${id}/cancel`, () => sampleResponse("steward/cancel-idle"));
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options });
    stopRun(id);
    await waitFor(() => expect(getRunSnapshot().runs.get(id)?.phase).toBe("running"));
    expect(api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/messages`)[0]?.signal?.aborted).toBe(false);
    steward.runs[0]?.stream.send("done", { type: "done", step: 1, end: { reason: "completed" } });
    steward.runs[0]?.stream.close();
    await waitFor(() => expect(getRunSnapshot().ends.get(id)).toEqual({ kind: "done", end: { reason: "completed" } }));
  });

  it("登出：中止所有运行并清空，不再写入", async () => {
    const { api, steward, refresh, options } = setup();
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options });
    const signal = api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/messages`)[0]?.signal;
    resetRuns();
    expect(signal?.aborted).toBe(true);
    steward.runs[0]?.stream.send("text", { type: "text", step: 1, text: "迟到的文字" });
    expect(getRunSnapshot().runs.size).toBe(0);
    expect(getRunSnapshot().ends.size).toBe(0);
    expect(refresh).not.toHaveBeenCalled();
  });

  it("收到方案时交给调用方；以 awaiting_confirmation 结束", async () => {
    const { steward, options } = setup();
    const onPlan = vi.fn();
    await startRun({ conversationId: id, text: "整理这张", baseSequence: 0, ...options, onPlan });
    const plan = planPayload(planOf("00000000-0000-4000-8000-0000000000c1", id, [{ id: "00000000-0000-4000-8000-0000000000b1", call_id: "c1", tool: "correct_release", target: "x", changes: [], revertible: true }]));
    steward.runs[0]?.stream.send("plan", { type: "plan", step: 1, plan });
    steward.runs[0]?.stream.send("done", { type: "done", step: 1, end: { reason: "awaiting_confirmation" } });
    steward.runs[0]?.stream.close();
    await waitFor(() => expect(getRunSnapshot().ends.get(id)).toEqual({ kind: "done", end: { reason: "awaiting_confirmation" } }));
    expect(onPlan).toHaveBeenCalledWith(plan);
  });
});

describe("对方案的决定（C6）", () => {
  const planId = "00000000-0000-4000-8000-0000000000c1";
  const item = { id: "00000000-0000-4000-8000-0000000000b1", call_id: "c1", tool: "correct_release", target: "x", changes: [], revertible: true };
  const seed = [{ callId: "c1", name: "correct_release", input: {}, status: "awaiting_confirmation" as const, output: null, errorCode: null, durationMs: null, executed: false, textsBefore: 1 }];

  it("走确认接口的流，请求体是摘要、接受的项与不再询问的工具；运行从方案的条目开始", async () => {
    const { api, steward, options } = setup();
    steward.plans.push(planOf(planId, id, [item]));
    const body = { digest: steward.plans[0]?.digest ?? "", accepted_item_ids: [item.id], auto_approve_tools: ["correct_release"] };
    await expect(startRun({ conversationId: id, text: null, baseSequence: 2, ...options, decision: { planId, body, seed } })).resolves.toEqual({ started: true });
    expect(api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/plans/${planId}/decision`).map((request) => request.body)).toEqual([body]);
    expect(getRunSnapshot().runs.get(id)).toMatchObject({ userText: null, view: { items: [{ callId: "c1", status: "awaiting_confirmation" }] } });
    steward.decisions[0]?.stream.send("tool_finished", { type: "tool_finished", step: 1, tool_finished: { call_id: "c1", status: "succeeded", output: { operation_id: "o" }, truncated: false, untrusted: false } });
    await waitFor(() => expect(getRunSnapshot().runs.get(id)?.view.items[0]?.status).toBe("succeeded"));
  });

  it("开流前被拒绝：返回原因，不记结束信息（由方案卡片就地说明）", async () => {
    const { steward, options } = setup();
    steward.plans.push(planOf(planId, id, [item]));
    const result = await startRun({ conversationId: id, text: null, baseSequence: 2, ...options, decision: { planId, body: { digest: "旧的", accepted_item_ids: [], auto_approve_tools: [] }, seed } });
    expect(result).toMatchObject({ started: false, failure: { kind: "http", status: 409, code: "plan_digest_mismatch" } });
    expect(getRunSnapshot().ends.has(id)).toBe(false);
    expect(steward.plans[0]?.status).toBe("pending");
  });
});

describe("重新审查（C7）", () => {
  const planId = "00000000-0000-4000-8000-0000000000c1";
  const item = { id: "00000000-0000-4000-8000-0000000000b1", call_id: "c1", tool: "correct_release", target: "x", changes: [], revertible: true };
  /** 重新审查只用于 Steward 模式的会话。 */
  const stewardMode = (steward: ReturnType<typeof setup>["steward"]) => {
    const conversation = steward.conversations[0];
    if (conversation !== undefined) conversation.mode = "steward";
  };
  const seed = [{ callId: "c1", name: "correct_release", input: {}, status: "awaiting_confirmation" as const, output: null, errorCode: null, durationMs: null, executed: false, textsBefore: 1 }];

  it("走审查接口的流（没有请求体）；审查的开始与结论交给调用方，批准的项落在方案的条目上", async () => {
    const { api, steward, options } = setup();
    stewardMode(steward);
    steward.plans.push(planOf(planId, id, [item]));
    const onReview = vi.fn();
    await expect(startRun({ conversationId: id, text: null, baseSequence: 2, ...options, onReview, review: { planId, seed } })).resolves.toEqual({ started: true });
    expect(api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/plans/${planId}/review`).map((request) => request.body)).toEqual([undefined]);
    const stream = steward.reviews[0]?.stream;
    const review = reviewOf(planId, [{ item_id: item.id, decision: "approved", risk: "low", reasons: ["可以撤销"], source: "model" }]);
    stream?.send("review_started", { type: "review_started", plan_id: planId, model: "m" });
    stream?.send("review_finished", { type: "review_finished", review });
    stream?.send("tool_started", { type: "tool_started", tool_started: { call_id: "c1", name: "correct_release", input: {} } });
    stream?.send("tool_finished", { type: "tool_finished", tool_finished: { call_id: "c1", status: "succeeded", output: { operation_id: "o" }, truncated: false, untrusted: false } });
    await waitFor(() => expect(getRunSnapshot().runs.get(id)?.view.items).toEqual([expect.objectContaining({ callId: "c1", status: "succeeded" })]));
    expect(onReview.mock.calls.map(([event]) => (event as { type: string }).type)).toEqual(["review_started", "review_finished"]);
  });

  it("流结束时通知调用方（done 不论原因、没有 done 就断开），在重读会话之前：审查中被取消或保存失败时据此清掉“审查中”", async () => {
    const { steward, refresh, options } = setup();
    stewardMode(steward);
    steward.plans.push(planOf(planId, id, [item]));
    const order: string[] = [];
    refresh.mockImplementation(() => {
      order.push("refresh");
      return Promise.resolve();
    });
    const onEnd = () => order.push("end");
    await startRun({ conversationId: id, text: null, baseSequence: 2, ...options, onEnd, review: { planId, seed } });
    steward.reviews[0]?.stream.send("review_started", { type: "review_started", plan_id: planId, model: "m" });
    steward.reviews[0]?.stream.send("done", { type: "done", end: { reason: "cancelled" } });
    steward.reviews[0]?.stream.close();
    await waitFor(() => expect(getRunSnapshot().runs.has(id)).toBe(false));
    expect(order).toEqual(["end", "refresh"]);

    order.length = 0;
    await startRun({ conversationId: id, text: null, baseSequence: 2, ...options, onEnd, review: { planId, seed } });
    steward.reviews[1]?.stream.send("review_started", { type: "review_started", plan_id: planId, model: "m" });
    // 断开后接回得到 204：以落库内容为准，同样通知。
    steward.reviews[1]?.stream.fail();
    await waitFor(() => expect(getRunSnapshot().runs.has(id)).toBe(false));
    expect(order).toEqual(["end", "refresh"]);
  });

  it("会话不是 Steward 模式：409 review_not_applicable", async () => {
    const { steward, options } = setup();
    steward.plans.push(planOf(planId, id, [item]));
    const result = await startRun({ conversationId: id, text: null, baseSequence: 2, ...options, review: { planId, seed } });
    expect(result).toMatchObject({ started: false, failure: { kind: "http", status: 409, code: "review_not_applicable" } });
  });

  it("Steward 模式的确认带免确认工具：400 invalid_input，方案仍在等待", async () => {
    const { steward, options } = setup();
    stewardMode(steward);
    steward.plans.push(planOf(planId, id, [item]));
    const body = { digest: steward.plans[0]?.digest ?? "", accepted_item_ids: [item.id], auto_approve_tools: ["correct_release"] };
    const result = await startRun({ conversationId: id, text: null, baseSequence: 2, ...options, decision: { planId, body, seed } });
    expect(result).toMatchObject({ started: false, failure: { kind: "http", status: 400, code: "invalid_input" } });
    expect(steward.plans[0]?.status).toBe("pending");
  });

  it("开流前被拒绝：返回原因，不记结束信息", async () => {
    const { steward, options } = setup();
    steward.plans.push(planOf(planId, id, [item], { expires_at: "2026-01-01T00:00:00Z" }));
    const result = await startRun({ conversationId: id, text: null, baseSequence: 2, ...options, review: { planId, seed } });
    expect(result).toMatchObject({ started: false, failure: { kind: "http", status: 409, code: "plan_expired" } });
    expect(getRunSnapshot().ends.has(id)).toBe(false);
  });
});

describe("接回运行（C10）", () => {
  const marker = "2026-11-16T00:00:05.123456Z";

  it("从头回放再实时推送；结束后重读并移除", async () => {
    const { api, steward, refresh, options } = setup();
    steward.eventLogs.add(id);
    const store = createRunStore();
    try {
      expect(store.attachRun({ conversationId: id, baseSequence: 3, marker, ...options })).toBe(true);
      expect(store.getSnapshot().runs.get(id)).toMatchObject({ userText: null, baseSequence: 3, phase: "running" });
      await waitFor(() => expect(steward.attaches).toHaveLength(1));
      expect(steward.attaches[0]?.after).toBe(0);
      expect(api.requestsTo("GET", `/api/v1/me/steward/conversations/${id}/events`)[0]?.headers.has("Last-Event-ID")).toBe(false);
      // 已经接着的会话不再接一次。
      expect(store.attachRun({ conversationId: id, baseSequence: 3, marker, ...options })).toBe(false);
      const stream = steward.attaches[0]?.stream;
      stream?.send("text", { type: "text", seq: 20, step: 1, text: "合并后的整段" });
      stream?.send("tool_started", { type: "tool_started", seq: 21, step: 1, tool_started: { call_id: "c1", name: "list_albums", input: {} } });
      await waitFor(() => expect(store.getSnapshot().runs.get(id)?.view.items).toHaveLength(1));
      stream?.send("done", { type: "done", seq: 22, step: 1, end: { reason: "completed" } });
      stream?.close();
      await waitFor(() => expect(store.getSnapshot().runs.has(id)).toBe(false));
      expect(refresh).toHaveBeenCalledTimes(1);
      expect(store.getSnapshot().ends.get(id)).toEqual({ kind: "done", end: { reason: "completed" } });
    } finally {
      store.reset();
    }
  });

  it("204：记下接不回来，同一版本的会话详情不再自动接回；新的版本或新的运行可以", async () => {
    const { steward, refresh, options } = setup();
    const store = createRunStore();
    try {
      store.attachRun({ conversationId: id, baseSequence: 0, marker, ...options });
      await waitFor(() => expect(store.getSnapshot().runs.has(id)).toBe(false));
      expect(store.getSnapshot().detached.get(id)).toBe(marker);
      expect(store.getSnapshot().ends.has(id)).toBe(false);
      expect(refresh).toHaveBeenCalledTimes(1);
      expect(store.attachRun({ conversationId: id, baseSequence: 0, marker, ...options })).toBe(false);

      steward.eventLogs.add(id);
      expect(store.attachRun({ conversationId: id, baseSequence: 0, marker: "2026-11-16T00:01:00Z", ...options })).toBe(true);
      expect(store.getSnapshot().detached.has(id)).toBe(false);
    } finally {
      store.reset();
    }
  });

  it("两个标签页：都能看到进度；在接回的那一页停止，两页都收到同一个 done（cancelled）", async () => {
    const { api, steward, options } = setup();
    steward.eventLogs.add(id);
    const first: RunStore = createRunStore();
    const second: RunStore = createRunStore();
    try {
      await first.startRun({ conversationId: id, text: "整理一下", baseSequence: 0, ...options });
      second.attachRun({ conversationId: id, baseSequence: 1, marker, ...options });
      await waitFor(() => expect(steward.attaches).toHaveLength(1));
      // 服务端把同一个事件推给每一条连接。
      for (const stream of [steward.runs[0]?.stream, steward.attaches[0]?.stream]) stream?.send("text", { type: "text", seq: 5, step: 1, text: "进行中" });
      await waitFor(() => expect(first.getSnapshot().runs.get(id)?.view.texts).toEqual(["进行中"]));
      await waitFor(() => expect(second.getSnapshot().runs.get(id)?.view.texts).toEqual(["进行中"]));

      second.stopRun(id);
      expect(second.getSnapshot().runs.get(id)?.phase).toBe("stopping");
      await waitFor(() => expect(first.getSnapshot().ends.get(id)).toEqual({ kind: "done", end: { reason: "cancelled" } }));
      await waitFor(() => expect(second.getSnapshot().ends.get(id)).toEqual({ kind: "done", end: { reason: "cancelled" } }));
      expect(api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/cancel`)).toHaveLength(1);
    } finally {
      first.reset();
      second.reset();
    }
  });

  it("登出：中止接回的连接与等待中的重试，不再写入", async () => {
    const { steward, refresh, options } = setup();
    steward.eventLogs.add(id);
    const store = createRunStore({ reconnectDelays: [60_000] });
    store.attachRun({ conversationId: id, baseSequence: 0, marker, ...options });
    await waitFor(() => expect(steward.attaches).toHaveLength(1));
    const signal = steward.attaches[0]?.request.signal;
    store.reset();
    expect(signal?.aborted).toBe(true);
    steward.attaches[0]?.stream.send("text", { type: "text", seq: 1, step: 1, text: "迟到的文字" });
    expect(store.getSnapshot().runs.size).toBe(0);

    // 等待重试期间登出：不再接回。
    store.attachRun({ conversationId: id, baseSequence: 0, marker, ...options });
    await waitFor(() => expect(steward.attaches).toHaveLength(2));
    steward.attaches[1]?.stream.fail();
    await waitFor(() => expect(store.getSnapshot().runs.get(id)?.reconnecting).toBe(true));
    store.reset();
    expect(store.getSnapshot().runs.size).toBe(0);
    expect(steward.attaches).toHaveLength(2);
    expect(refresh).not.toHaveBeenCalled();
  });

  it("接回的流断开：同样带着序号重新接回", async () => {
    const { steward, options } = setup();
    steward.eventLogs.add(id);
    const store = createRunStore();
    try {
      store.attachRun({ conversationId: id, baseSequence: 0, marker, ...options });
      await waitFor(() => expect(steward.attaches).toHaveLength(1));
      steward.attaches[0]?.stream.send("text", { type: "text", seq: 30, step: 1, text: "一" });
      await waitFor(() => expect(store.getSnapshot().runs.get(id)?.lastSeq).toBe(30));
      steward.attaches[0]?.stream.fail();
      await waitFor(() => expect(steward.attaches).toHaveLength(2));
      expect(steward.attaches[1]?.after).toBe(30);
    } finally {
      store.reset();
    }
  });

  it("没有 seq 的事件（旧后端）照常收下", async () => {
    const { steward, options } = setup();
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options });
    steward.runs[0]?.stream.send("text", { type: "text", step: 1, text: "甲" });
    steward.runs[0]?.stream.send("text", { type: "text", step: 1, text: "乙" });
    await waitFor(() => expect(getRunSnapshot().runs.get(id)?.view.texts).toEqual(["甲乙"]));
  });
});
