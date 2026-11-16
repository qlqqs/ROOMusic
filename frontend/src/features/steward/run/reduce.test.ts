import { describe, expect, it } from "vitest";
import type { MessageDTO, RunEvent } from "../api/decoders";
import {
  applyRunEvent,
  compactedTurnKeyOf,
  compareTimestamps,
  emptyRunView,
  formatElapsed,
  isIncomplete,
  isNewEvent,
  NATIVE_SEARCH,
  runBaseSequenceOf,
  summarizeSteps,
  turnsFromMessages,
  type RunView,
} from "./reduce";

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
  const item = (durationMs: number | null, executed = durationMs !== null) => ({ callId: "a", name: "x", input: {}, status: "succeeded" as const, output: null, errorCode: null, durationMs, executed, textsBefore: 0 });

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

describe("修改方案的条目（C6）", () => {
  const planEvent: RunEvent = {
    type: "plan",
    step: 1,
    plan: {
      plan_id: "00000000-0000-4000-8000-0000000000c1",
      digest: "abc",
      expires_at: "2026-11-17T00:00:00Z",
      items: [{ id: "00000000-0000-4000-8000-0000000000b1", call_id: "call-2", tool: "correct_release", target: "Bayreuth 1951", changes: [], revertible: true }],
    },
  };

  it("方案里的调用作为等待确认的条目放进过程，没有执行；它之前的文字段数是方案那一步", () => {
    const view = [
      { type: "text", step: 1, text: "我整理一下。" },
      { type: "tool_started", step: 1, tool_started: { call_id: "call-1", name: "get_release", input: {} } },
      { type: "tool_finished", step: 1, tool_finished: { call_id: "call-1", status: "succeeded", output: {}, truncated: false, untrusted: true } },
      // 后端先为方案里的调用发 tool_started，再发 plan。
      { type: "tool_started", step: 1, tool_started: { call_id: "call-2", name: "correct_release", input: {} } },
      planEvent,
      { type: "done", step: 1, end: { reason: "awaiting_confirmation" } },
    ].reduce<ReturnType<typeof applyRunEvent>>((current, event) => applyRunEvent(current, event as RunEvent), emptyRunView);
    expect(view.items.map((item) => [item.callId, item.name, item.status, item.executed, item.textsBefore])).toEqual([
      ["call-1", "get_release", "succeeded", true, 1],
      ["call-2", "correct_release", "awaiting_confirmation", false, 1],
    ]);
    expect(view.end).toEqual({ reason: "awaiting_confirmation" });
  });

  it("确认之后：等待确认的条目原地变成执行中，再按结果更新；没选的项按 declined_by_user 失败", () => {
    const seeded = applyRunEvent(emptyRunView, planEvent);
    const started = applyRunEvent(seeded, { type: "tool_started", step: 1, tool_started: { call_id: "call-2", name: "correct_release", input: { release_id: "r" } } });
    expect(started.items).toHaveLength(1);
    expect(started.items[0]).toMatchObject({ status: "running", input: { release_id: "r" }, executed: true });
    const done = applyRunEvent(started, { type: "tool_finished", step: 1, tool_finished: { call_id: "call-2", status: "succeeded", output: { operation_id: "o" }, truncated: false, untrusted: false } });
    expect(done.items[0]).toMatchObject({ status: "succeeded", output: { operation_id: "o" } });
    const declined = applyRunEvent(seeded, { type: "tool_finished", step: 1, tool_finished: { call_id: "call-2", status: "failed", output: null, truncated: false, untrusted: false, error_code: "declined_by_user" } });
    expect(declined.items[0]).toMatchObject({ status: "failed", errorCode: "declined_by_user", executed: false });
  });

  it("落库的等待确认调用没有耗时，不算执行过；文字段数取它所在的助手消息", () => {
    const turns = turnsFromMessages([
      message("user", [{ type: "text", text: "整理这张" }]),
      message("assistant", [{ type: "text", text: "建议改标题。" }, { type: "tool_call", tool_call: { id: "call-2", name: "correct_release", arguments: {} } }], [
        record("correct_release", { status: "awaiting_confirmation", output: null, duration_ms: null }),
      ]),
    ]);
    expect(turns[0]?.items.map((item) => [item.status, item.executed, item.textsBefore])).toEqual([["awaiting_confirmation", false, 1]]);
  });
});

describe("审查的事件（C7）", () => {
  it("送审的调用审查前已开始：批准后再次开始时原地更新，不重复；审查期间不显示“正在想…”", () => {
    const started = applyRunEvent(emptyRunView, { type: "tool_started", step: 1, tool_started: { call_id: "call-2", name: "correct_release", input: {} } });
    const reviewing = applyRunEvent(started, { type: "review_started", step: 1, plan_id: "00000000-0000-4000-8000-0000000000c1", model: "m" });
    expect(reviewing.waiting).toBe(false);
    const again = applyRunEvent(reviewing, { type: "tool_started", tool_started: { call_id: "call-2", name: "correct_release", input: { release_id: "r" } } });
    expect(again.items).toHaveLength(1);
    expect(again.items[0]).toMatchObject({ status: "running", input: { release_id: "r" }, textsBefore: 1 });
    const done = applyRunEvent(again, { type: "tool_finished", tool_finished: { call_id: "call-2", status: "succeeded", output: {}, truncated: false, untrusted: false } });
    expect(done.waiting).toBe(true);
  });

  it("审查结论之后：没有进行中的调用时等模型接着回答", () => {
    const review = { review_id: "00000000-0000-4000-8000-0000000000e1", plan_id: "00000000-0000-4000-8000-0000000000c1", model: "m", status: "completed" as const, policy_version: 1, expires_at: at, items: [] };
    expect(applyRunEvent({ ...emptyRunView, waiting: false }, { type: "review_finished", review }).waiting).toBe(true);
  });
});

describe("模型自带搜索（C8）", () => {
  const search = { search_id: "s1", query: "贝九 1951", status: "completed" as const, results: [{ title: "Bayreuth", url: "https://example.org/a" }, { url: "https://example.org/b" }] };

  it("search 事件是一条已结束的过程条目，citations 按地址去重合并", () => {
    const events = [
      { type: "search", step: 1, ...search },
      { type: "search", step: 1, search_id: "s2", query: "坏的", status: "failed", error_code: "max_uses_exceeded" },
      { type: "text", step: 1, text: "拜罗伊特那版是 1951 年。" },
      { type: "citations", step: 1, citations: [{ url: "https://example.org/a", title: "Bayreuth" }, { url: "https://example.org/b" }] },
      { type: "citations", step: 2, citations: [{ url: "https://example.org/a", title: "重复" }] },
    ] satisfies RunEvent[];
    const view = events.reduce<typeof emptyRunView>(applyRunEvent, emptyRunView);
    expect(view.items.map(({ callId, name, status, errorCode, executed, input }) => ({ callId, name, status, errorCode, executed, input }))).toEqual([
      { callId: "s1", name: NATIVE_SEARCH, status: "succeeded", errorCode: null, executed: true, input: { q: "贝九 1951" } },
      { callId: "s2", name: NATIVE_SEARCH, status: "failed", errorCode: "max_uses_exceeded", executed: true, input: { q: "坏的" } },
    ]);
    expect(view.items[0]?.output).toEqual({ results: [{ title: "Bayreuth", url: "https://example.org/a" }, { title: "", url: "https://example.org/b" }] });
    expect(view.items[1]?.output).toEqual({ results: [] });
    expect(view.citations).toEqual([{ url: "https://example.org/a", title: "Bayreuth" }, { url: "https://example.org/b", title: "" }]);
    expect(view.waiting).toBe(false);
  });

  it("落库的 searches 与 citations 还原成同样的条目与来源；没有这两项的消息按空处理", () => {
    const streamed = [{ type: "search", step: 1, ...search }, { type: "text", step: 1, text: "答" }, { type: "citations", step: 1, citations: [{ url: "https://example.org/a", title: "Bayreuth" }] }] satisfies RunEvent[];
    const view = streamed.reduce<typeof emptyRunView>(applyRunEvent, emptyRunView);
    const turns = turnsFromMessages([
      message("user", [{ type: "text", text: "贝九" }]),
      { ...message("assistant", [{ type: "text", text: "答" }]), searches: [search], citations: [{ url: "https://example.org/a", title: "Bayreuth" }] },
      message("user", [{ type: "text", text: "再问" }]),
      message("assistant", [{ type: "text", text: "没有搜索" }]),
    ]);
    expect(turns[0]?.items.map(({ callId, name, input, status, output }) => ({ callId, name, input, status, output }))).toEqual(
      view.items.map(({ callId, name, input, status, output }) => ({ callId, name, input, status, output })),
    );
    expect(turns[0]?.citations).toEqual(view.citations);
    expect(turns[1]).toMatchObject({ items: [], citations: [] });
  });
});

describe("事件序号与接回（C10）", () => {
  /** 与 run-store 相同：序号不大于最后一个的事件丢掉，其余归并并记下序号。 */
  function fold(start: { view: RunView; lastSeq: number }, events: RunEvent[]) {
    return events.reduce((state, event) => (isNewEvent(state.lastSeq, event) ? { view: applyRunEvent(state.view, event), lastSeq: event.seq ?? state.lastSeq } : state), start);
  }

  it("按序号去重：重复与更早的事件丢掉；没有序号的照常收下", () => {
    const state = fold({ view: emptyRunView, lastSeq: 0 }, [
      { type: "text", seq: 3, step: 1, text: "我查" },
      { type: "text", seq: 3, step: 1, text: "我查" },
      { type: "tool_started", seq: 2, step: 1, tool_started: { call_id: "old", name: "list_albums", input: {} } },
      { type: "text", step: 1, text: "一下" },
    ]);
    expect(state.view.texts).toEqual(["我查一下"]);
    expect(state.view.items).toEqual([]);
    expect(state.lastSeq).toBe(3);
  });

  it("回放里合并过的文字：序号是合并范围的末尾；断在中间时服务端给后缀，接上后与原文一致、不重复", () => {
    // 原来逐个收到的增量：seq 1“贝多芬”，seq 2“第九”，seq 3“交响曲”；收到 seq 2 之后断开。
    const before = fold({ view: emptyRunView, lastSeq: 0 }, [
      { type: "text", seq: 1, step: 1, text: "贝多芬" },
      { type: "text", seq: 2, step: 1, text: "第九" },
    ]);
    // 带 after=2 接回：合并的一条只给 seq 2 之后的部分，序号是 3。
    const resumed = fold(before, [{ type: "text", seq: 3, step: 1, text: "交响曲" }, { type: "done", seq: 4, step: 1, end: { reason: "completed" } }]);
    expect(resumed.view.texts).toEqual(["贝多芬第九交响曲"]);
    // 刷新后从头回放：同一步的三个增量合成一条，序号是 3。
    const replayed = fold({ view: emptyRunView, lastSeq: 0 }, [{ type: "text", seq: 3, step: 1, text: "贝多芬第九交响曲" }]);
    expect(replayed.view.texts).toEqual(resumed.view.texts);
  });

  it("history_gap：运行标为不完整，序号记到省略部分的末尾；history_compacted 记下摘要的分界", () => {
    const gap = fold({ view: emptyRunView, lastSeq: 0 }, [{ type: "history_gap", seq: 40 }]);
    expect(gap.view.gap).toBe(true);
    expect(gap.lastSeq).toBe(40);
    const through = "00000000-0000-4000-8000-0000000000aa";
    expect(applyRunEvent(emptyRunView, { type: "history_compacted", seq: 1, through_message_id: through }).compactedThrough).toBe(through);
  });

  it("时间戳按微秒比较：毫秒相同时看其余位，时区不同也按同一时刻比较", () => {
    expect(compareTimestamps("2026-11-16T00:00:00.123457Z", "2026-11-16T00:00:00.123456Z")).toBeGreaterThan(0);
    expect(compareTimestamps("2026-11-16T00:00:00.1234Z", "2026-11-16T00:00:00.123456Z")).toBeLessThan(0);
    expect(compareTimestamps("2026-11-16T08:00:00+08:00", "2026-11-16T00:00:00Z")).toBe(0);
    expect(compareTimestamps("2026-11-16T00:00:01Z", "2026-11-16T00:00:00.999999Z")).toBeGreaterThan(0);
  });

  describe("接回时已落库的消息保留到哪一条", () => {
    const finished = "2026-11-16T00:00:10.000005Z";
    const before = "2026-11-16T00:00:09Z";
    const after = "2026-11-16T00:00:10.000006Z";
    const at = (createdAt: string, overrides: Partial<MessageDTO>) => ({ ...message("assistant", []), created_at: createdAt, ...overrides });

    it("发消息开始的运行：保留到这次的用户消息，之后的由回放显示", () => {
      const messages = [
        at(before, { sequence: 1, role: "user" }),
        at(before, { sequence: 2, role: "assistant" }),
        at(after, { sequence: 3, role: "user" }),
        at(after, { sequence: 4, role: "assistant" }),
        at(after, { sequence: 5, role: "tool" }),
      ];
      expect(runBaseSequenceOf({ messages, last_run_finished_at: finished })).toBe(3);
    });

    it("确认方案或继续开始的运行：保留到上一次运行的最后一条", () => {
      const messages = [at(before, { sequence: 1, role: "user" }), at(before, { sequence: 2, role: "assistant" }), at(after, { sequence: 3, role: "tool" })];
      expect(runBaseSequenceOf({ messages, last_run_finished_at: finished })).toBe(2);
    });

    it("这次运行还没有落库任何消息：保留全部；从没结束过运行（第一次）：保留第一条用户消息", () => {
      const messages = [at(before, { sequence: 1, role: "user" }), at(before, { sequence: 2, role: "assistant" })];
      expect(runBaseSequenceOf({ messages, last_run_finished_at: finished })).toBe(2);
      expect(runBaseSequenceOf({ messages, last_run_finished_at: null })).toBe(1);
      expect(runBaseSequenceOf({ messages: [], last_run_finished_at: null })).toBe(0);
    });
  });

  it("摘要分界所在的回合：按 turnsFromMessages 的 key；找不到消息时是 null", () => {
    const messages = [
      message("user", [{ type: "text", text: "一" }]),
      message("assistant", [{ type: "text", text: "答一" }]),
      message("user", [{ type: "text", text: "二" }]),
      message("assistant", [{ type: "text", text: "答二" }]),
    ];
    const turns = turnsFromMessages(messages);
    expect(compactedTurnKeyOf(messages, messages[1]?.id ?? "")).toBe(turns[0]?.key);
    expect(compactedTurnKeyOf(messages, messages[3]?.id ?? "")).toBe(turns[1]?.key);
    expect(compactedTurnKeyOf(messages, "00000000-0000-4000-8000-00000000ffff")).toBeNull();
  });
});
