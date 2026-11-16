import { describe, expect, it } from "vitest";
import type { MessageDTO, RunEvent } from "../api/decoders";
import { applyRunEvent, emptyRunView, formatElapsed, isIncomplete, summarizeSteps, turnsFromMessages } from "./reduce";

const at = "2026-11-16T00:00:00Z";
let sequence = 0;
function message(role: MessageDTO["role"], content: MessageDTO["content"], toolCalls: MessageDTO["tool_calls"] = []): MessageDTO {
  sequence += 1;
  return {
    id: `00000000-0000-4000-8000-${String(sequence).padStart(12, "0")}`,
    sequence,
    role,
    content,
    input_tokens: null,
    output_tokens: null,
    created_at: at,
    tool_calls: toolCalls,
  };
}

const record = (name: string, overrides: Partial<MessageDTO["tool_calls"][number]> = {}): MessageDTO["tool_calls"][number] => ({
  id: "00000000-0000-4000-8000-0000000000aa",
  name,
  input: {},
  output: { items: [], pagination: { page: 1, page_size: 20, total: 2 } },
  truncated: false,
  untrusted: true,
  status: "succeeded",
  error_code: null,
  duration_ms: 1200,
  ...overrides,
});

describe("运行事件归并", () => {
  it("文字按步骤追加，工具调用先进行中、再按 call_id 更新，done 记结束原因", () => {
    const events: RunEvent[] = [
      { type: "text", step: 1, text: "我先" },
      { type: "text", step: 1, text: "查一下。" },
      { type: "usage", step: 1, usage: { input_tokens: 10, output_tokens: 3 } },
      { type: "tool_started", step: 1, tool_started: { call_id: "call-1", name: "search_library", input: { q: "Furtwängler", type: "artists" } } },
      { type: "tool_started", step: 1, tool_started: { call_id: "call-2", name: "get_album", input: { id: "x" } } },
      { type: "tool_finished", step: 1, tool_finished: { call_id: "call-2", status: "failed", output: { code: "not_found" }, truncated: false, untrusted: false, error_code: "not_found" } },
      { type: "text", step: 3, text: "找到了。" },
    ];
    const view = events.reduce(applyRunEvent, emptyRunView);
    expect(view.texts).toEqual(["我先查一下。", "", "找到了。"]);
    expect(view.items.map((item) => [item.callId, item.status, item.errorCode])).toEqual([
      ["call-1", "running", null],
      ["call-2", "failed", "not_found"],
    ]);
    expect(view.end).toBeNull();
    expect(view.items.every((item) => item.executed)).toBe(true);
    expect(applyRunEvent(view, { type: "done", step: 3, end: { reason: "completed" } }).end).toEqual({ reason: "completed" });
  });

  it("等模型输出：刚开始、这一步的工具都执行完时为真；有文字、有工具在执行、结束后为假", () => {
    expect(emptyRunView.waiting).toBe(true);
    const texting = applyRunEvent(emptyRunView, { type: "text", step: 1, text: "我查" });
    expect(texting.waiting).toBe(false);
    const started = [
      { type: "tool_started", step: 1, tool_started: { call_id: "a", name: "list_albums", input: {} } },
      { type: "tool_started", step: 1, tool_started: { call_id: "b", name: "list_albums", input: {} } },
    ] satisfies RunEvent[];
    const running = started.reduce<typeof emptyRunView>(applyRunEvent, texting);
    expect(running.waiting).toBe(false);
    const finish = (callId: string): RunEvent => ({ type: "tool_finished", step: 1, tool_finished: { call_id: callId, status: "succeeded", output: {}, truncated: false, untrusted: true } });
    const oneLeft = applyRunEvent(running, finish("a"));
    expect(oneLeft.waiting).toBe(false);
    const allDone = applyRunEvent(oneLeft, finish("b"));
    expect(allDone.waiting).toBe(true);
    expect(applyRunEvent(allDone, { type: "done", step: 1, end: { reason: "token_limit" } }).waiting).toBe(false);
  });

  it("没有对应 tool_started 的 tool_finished 不新增条目", () => {
    const view = applyRunEvent(emptyRunView, { type: "tool_finished", step: 1, tool_finished: { call_id: "x", status: "succeeded", output: {}, truncated: false, untrusted: true } });
    expect(view.items).toEqual([]);
  });
});

describe("已落库消息还原成回合", () => {
  it("用户消息开新回合；助手消息的工具调用按下标对上存储的调用记录；工具消息不单独显示", () => {
    const messages = [
      message("user", [{ type: "text", text: "富特文格勒的贝九有几版？" }]),
      message(
        "assistant",
        [
          { type: "text", text: "我查一下。" },
          { type: "tool_call", tool_call: { id: "call-1", name: "search_library", arguments: { q: "Furtwängler", type: "artists" } } },
          { type: "tool_call", tool_call: { id: "call-2", name: "get_track", arguments: { id: "y" } } },
        ],
        [record("search_library"), record("get_track", { status: "failed", error_code: "not_found", output: { code: "not_found" }, duration_ms: 30 })],
      ),
      message("tool", [{ type: "tool_result", tool_result: { call_id: "call-1", content: "{}", is_error: false } }]),
      message("tool", [{ type: "tool_result", tool_result: { call_id: "call-2", content: "{}", is_error: true } }]),
      message("assistant", [{ type: "text", text: "一共 3 版。" }]),
      message("user", [{ type: "text", text: "1942 那版呢？" }]),
    ];
    const turns = turnsFromMessages(messages);
    expect(turns).toHaveLength(2);
    expect(turns[0]?.userText).toBe("富特文格勒的贝九有几版？");
    expect(turns[0]?.texts).toEqual(["我查一下。", "一共 3 版。"]);
    expect(turns[0]?.items.map((item) => [item.callId, item.name, item.status, item.errorCode, item.durationMs])).toEqual([
      ["call-1", "search_library", "succeeded", null, 1200],
      ["call-2", "get_track", "failed", "not_found", 30],
    ]);
    expect(turns[0]?.items[0]?.input).toEqual({ q: "Furtwängler", type: "artists" });
    expect(turns[1]).toMatchObject({ userText: "1942 那版呢？", items: [], texts: [] });
    expect(isIncomplete(messages)).toBe(true);
    expect(isIncomplete(messages.slice(0, 5))).toBe(false);
    expect(isIncomplete([])).toBe(false);
  });

  it("流式归并与落库还原得到同样的过程与文字", () => {
    const streamed = [
      { type: "text", step: 1, text: "我查一下。" },
      { type: "tool_started", step: 1, tool_started: { call_id: "call-1", name: "list_albums", input: { q: "贝九" } } },
      { type: "tool_finished", step: 1, tool_finished: { call_id: "call-1", status: "succeeded", output: { items: [1, 2] }, truncated: false, untrusted: true } },
      { type: "text", step: 2, text: "两张。" },
    ] satisfies RunEvent[];
    const view = streamed.reduce<typeof emptyRunView>(applyRunEvent, emptyRunView);
    const turns = turnsFromMessages([
      message("user", [{ type: "text", text: "贝九" }]),
      message("assistant", [{ type: "text", text: "我查一下。" }, { type: "tool_call", tool_call: { id: "call-1", name: "list_albums", arguments: { q: "贝九" } } }], [record("list_albums", { output: { items: [1, 2] } })]),
      message("tool", [{ type: "tool_result", tool_result: { call_id: "call-1", content: "{}", is_error: false } }]),
      message("assistant", [{ type: "text", text: "两张。" }]),
    ]);
    expect(turns[0]?.texts).toEqual(view.texts);
    expect(turns[0]?.items.map(({ callId, name, input, status, output }) => ({ callId, name, input, status, output }))).toEqual(
      view.items.map(({ callId, name, input, status, output }) => ({ callId, name, input, status, output })),
    );
  });
});

describe("过程摘要", () => {
  const item = (durationMs: number | null, executed = durationMs !== null) => ({ callId: "a", name: "x", input: {}, status: "succeeded" as const, output: null, errorCode: null, durationMs, executed });

  it("只统计实际执行过的条目，用时是它们的耗时之和", () => {
    expect(summarizeSteps([item(20_000), item(null), item(1_400)])).toEqual({ count: 2, durationMs: 21_400 });
  });

  it("流式收到的条目都执行过但还没有耗时：只给次数，用时为 null", () => {
    expect(summarizeSteps([item(null, true), item(null, true)])).toEqual({ count: 2, durationMs: null });
  });

  it("落库的未执行调用（token_limit）没有耗时，不算“查了”", () => {
    const turns = turnsFromMessages([
      message("user", [{ type: "text", text: "问" }]),
      message(
        "assistant",
        [{ type: "tool_call", tool_call: { id: "a", name: "list_albums", arguments: {} } }, { type: "tool_call", tool_call: { id: "b", name: "list_albums", arguments: {} } }],
        [record("list_albums"), record("list_albums", { status: "failed", error_code: "token_limit", output: { code: "token_limit" }, duration_ms: null })],
      ),
    ]);
    expect(turns[0]?.items.map((entry) => entry.executed)).toEqual([true, false]);
    expect(summarizeSteps(turns[0]?.items ?? [])).toEqual({ count: 1, durationMs: 1200 });
  });

  it.each([
    [0, "不到 1 秒"],
    [999, "不到 1 秒"],
    [21_400, "21 秒"],
    [60_000, "1 分钟"],
    [125_000, "2 分 5 秒"],
  ])("%d 毫秒写作“%s”", (ms, text) => {
    expect(formatElapsed(ms)).toBe(text);
  });
});
