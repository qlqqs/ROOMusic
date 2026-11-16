import { waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { installFakeApi } from "../../../test/fake-api";
import { conversationOf, installStewardApi } from "../../../test/fake-steward";
import { sampleResponse } from "../../../test/http";
import { getRunSnapshot, resetRuns, startRun, stopRun } from "./run-store";

const id = "00000000-0000-4000-8000-000000000001";

function setup() {
  const api = installFakeApi();
  const steward = installStewardApi(api, { conversations: [conversationOf(id)] });
  const refresh = vi.fn(() => Promise.resolve());
  return { api, steward, refresh };
}

afterEach(() => resetRuns());

describe("进行中的运行", () => {
  it("发消息：流开起来后逐个归并事件；done 之后重读会话，再从 store 移除并留下结束信息", async () => {
    const { api, steward, refresh } = setup();
    const result = startRun({ conversationId: id, text: "贝九有几版？", baseSequence: 4, refresh });
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
    const { api, refresh } = setup();
    await startRun({ conversationId: id, text: null, baseSequence: 2, refresh });
    await expect(startRun({ conversationId: id, text: null, baseSequence: 2, refresh })).resolves.toEqual({ started: false, failure: null });
    const requests = api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/continue`);
    expect(requests).toHaveLength(1);
    expect(requests[0]?.body).toBeUndefined();
  });

  it("开始流之前被拒绝：没有运行，结束信息是拒绝的原因", async () => {
    const { steward, refresh } = setup();
    steward.nextRunResponse = sampleResponse("steward/conversation-busy");
    const result = await startRun({ conversationId: id, text: "你好", baseSequence: 0, refresh });
    expect(result).toMatchObject({ started: false, failure: { kind: "http", status: 409, code: "conversation_busy" } });
    expect(getRunSnapshot().runs.has(id)).toBe(false);
    expect(getRunSnapshot().ends.get(id)).toMatchObject({ kind: "request_failed", failure: { code: "conversation_busy" } });
  });

  it("流没有 done 就断了：结束信息是 disconnected", async () => {
    const { steward, refresh } = setup();
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, refresh });
    steward.runs[0]?.stream.fail();
    await waitFor(() => expect(getRunSnapshot().ends.get(id)).toEqual({ kind: "disconnected" }));
    await waitFor(() => expect(getRunSnapshot().runs.has(id)).toBe(false));
  });

  it("停止：调用取消接口，服务端发来 done（cancelled）后结束", async () => {
    const { api, refresh } = setup();
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, refresh });
    stopRun(id);
    expect(getRunSnapshot().runs.get(id)?.phase).toBe("stopping");
    await waitFor(() => expect(getRunSnapshot().ends.get(id)).toEqual({ kind: "done", end: { reason: "cancelled" } }));
    expect(api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/cancel`)).toHaveLength(1);
  });

  it("停止时取消接口说没有这次运行：直接断开，也算已停止", async () => {
    const { api, refresh } = setup();
    api.on("POST", `/api/v1/me/steward/conversations/${id}/cancel`, () => sampleResponse("steward/cancel-idle"));
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, refresh });
    stopRun(id);
    await waitFor(() => expect(getRunSnapshot().ends.get(id)).toEqual({ kind: "done", end: { reason: "cancelled" } }));
  });

  it("登出：中止所有运行并清空，不再写入", async () => {
    const { api, steward, refresh } = setup();
    await startRun({ conversationId: id, text: "你好", baseSequence: 0, refresh });
    const signal = api.requestsTo("POST", `/api/v1/me/steward/conversations/${id}/messages`)[0]?.signal;
    resetRuns();
    expect(signal?.aborted).toBe(true);
    steward.runs[0]?.stream.send("text", { type: "text", step: 1, text: "迟到的文字" });
    expect(getRunSnapshot().runs.size).toBe(0);
    expect(getRunSnapshot().ends.size).toBe(0);
    expect(refresh).not.toHaveBeenCalled();
  });
});
