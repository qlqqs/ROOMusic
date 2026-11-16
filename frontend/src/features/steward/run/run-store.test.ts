import { waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { installFakeApi } from "../../../test/fake-api";
import { conversationOf, installStewardApi, planOf, planPayload } from "../../../test/fake-steward";
import { sampleResponse } from "../../../test/http";
import { getRunSnapshot, resetRuns, startRun, stopRun } from "./run-store";

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

  it("流没有 done 就断了：结束信息是 disconnected", async () => {
    const { steward, options } = setup();
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options });
    steward.runs[0]?.stream.fail();
    await waitFor(() => expect(getRunSnapshot().ends.get(id)).toEqual({ kind: "disconnected" }));
    await waitFor(() => expect(getRunSnapshot().runs.has(id)).toBe(false));
  });

  it("停止：调用取消接口，服务端发来 done（cancelled）后结束", async () => {
    const { api, options } = setup();
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options });
    stopRun(id);
    expect(getRunSnapshot().runs.get(id)?.phase).toBe("stopping");
    await waitFor(() => expect(getRunSnapshot().ends.get(id)).toEqual({ kind: "done", end: { reason: "cancelled" } }));
    expect(api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/cancel`)).toHaveLength(1);
  });

  it("停止时取消接口说没有这次运行：直接断开，也算已停止", async () => {
    const { api, options } = setup();
    api.on("POST", `/api/v1/me/steward/conversations/${id}/cancel`, () => sampleResponse("steward/cancel-idle"));
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, ...options });
    stopRun(id);
    await waitFor(() => expect(getRunSnapshot().ends.get(id)).toEqual({ kind: "done", end: { reason: "cancelled" } }));
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
