import { describe, expect, it } from "vitest";
import {
  decodeConversationDetail,
  decodeConversationPage,
  decodeConversationPatch,
  decodeMarkedRead,
  decodeMemory,
  decodeMemoryList,
  decodeRunEvent,
  decodeSearchTest,
  decodeSettings,
  effectiveNativeSearch,
  operationIdOf,
} from "./decoders";
import { searchSettingsDefaults, settingsOf } from "../../../test/fake-steward";

const planItem = {
  id: "00000000-0000-4000-8000-0000000000b1",
  call_id: "call-1",
  tool: "correct_release",
  target: "Bayreuth 1951",
  changes: [{ field: "title", before: "Beethoven Symphony No.9", after: "贝多芬：第九交响曲" }, { field: "year", before: null, after: 1951 }],
  revertible: true,
  basis: { source: "external", note: "来自 MusicBrainz" },
};
const plan = { plan_id: "00000000-0000-4000-8000-0000000000c1", digest: "abc", expires_at: "2026-11-17T00:00:00Z", items: [planItem] };

describe("运行流的事件", () => {
  it("按事件名解码五种事件", () => {
    expect(decodeRunEvent("text", JSON.stringify({ type: "text", step: 1, text: "你好" }))).toEqual({ type: "text", step: 1, text: "你好" });
    expect(decodeRunEvent("tool_finished", JSON.stringify({
      type: "tool_finished",
      step: 2,
      tool_finished: { call_id: "c1", status: "failed", output: { code: "not_found", message: "目标不存在" }, truncated: false, untrusted: true, error_code: "not_found" },
    }))?.type).toBe("tool_finished");
    expect(decodeRunEvent("done", JSON.stringify({ type: "done", end: { error_code: "database_unavailable", message: "存储失败" } }))).toEqual({
      type: "done",
      end: { error_code: "database_unavailable", message: "存储失败" },
    });
    expect(decodeRunEvent("done", JSON.stringify({ type: "done", step: 3, end: { reason: "provider_error", error_kind: "other", error_code: "provider_unreachable", status_code: 529, message: "Overloaded" } }))).toMatchObject({
      end: { reason: "provider_error", status_code: 529 },
    });
  });

  it("修改方案（C6）：plan 事件带方案与各项；没有依据的项可以省略 basis；没有项的方案被拒绝", () => {
    expect(decodeRunEvent("plan", JSON.stringify({ type: "plan", step: 2, plan }))).toEqual({ type: "plan", step: 2, plan });
    const { basis: _basis, ...bare } = planItem;
    expect(decodeRunEvent("plan", JSON.stringify({ type: "plan", step: 1, plan: { ...plan, items: [bare] } }))).toMatchObject({ plan: { items: [{ id: planItem.id }] } });
    expect(() => decodeRunEvent("plan", JSON.stringify({ type: "plan", step: 1, plan: { ...plan, items: [] } }))).toThrow();
    expect(() => decodeRunEvent("plan", JSON.stringify({ type: "plan", step: 1, plan: { ...plan, items: [{ ...planItem, basis: { source: "web", note: "" } }] } }))).toThrow();
    expect(decodeRunEvent("done", JSON.stringify({ type: "done", step: 2, end: { reason: "awaiting_confirmation" } }))).toEqual({ type: "done", step: 2, end: { reason: "awaiting_confirmation" } });
  });

  it("确认后执行方案项的 tool_started、tool_finished 没有 step", () => {
    expect(decodeRunEvent("tool_finished", JSON.stringify({ type: "tool_finished", tool_finished: { call_id: "call-1", status: "succeeded", output: { operation_id: "o" }, truncated: false, untrusted: false } }))).toMatchObject({
      type: "tool_finished",
      tool_finished: { call_id: "call-1" },
    });
  });

  it("会话详情的 plans 带已处理方案的状态、每项的取舍与操作记录", () => {
    const base = { id: "00000000-0000-4000-8000-000000000001", title: "", mode: "assistant", model: "m", status: "idle", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T00:00:00Z", messages: [], pending_plan: null };
    const applied = { ...plan, status: "applied", items: [{ ...planItem, decision: "accepted", operation_id: "00000000-0000-4000-8000-0000000000d1", result: { ok: true } }] };
    expect(decodeConversationDetail({ ...base, plans: [applied] }).plans?.[0]).toMatchObject({ status: "applied", items: [{ decision: "accepted", operation_id: "00000000-0000-4000-8000-0000000000d1" }] });
    expect(() => decodeConversationDetail({ ...base, plans: [{ ...applied, status: "done" }] })).toThrow();
  });

  it("已执行的方案项从工具输出取 operation_id；没有时是 null", () => {
    expect(operationIdOf({ operation_id: "00000000-0000-4000-8000-0000000000d1", revision: 3 })).toBe("00000000-0000-4000-8000-0000000000d1");
    expect(operationIdOf({ revision: 3 })).toBeNull();
    expect(operationIdOf("{\"operation_id\":")).toBeNull();
  });

  it("不认识的事件名丢掉；data 与事件名不符时抛出", () => {
    expect(decodeRunEvent("message", "{}")).toBeNull();
    expect(decodeRunEvent("toString", "{}")).toBeNull();
    expect(() => decodeRunEvent("text", JSON.stringify({ type: "usage", step: 1, text: "x" }))).toThrow();
    expect(() => decodeRunEvent("text", "不是 JSON")).toThrow();
  });

  it("很长的文字增量不被字符串上限拒绝", () => {
    const text = "长".repeat(10_000);
    expect(decodeRunEvent("text", JSON.stringify({ type: "text", step: 1, text }))).toEqual({ type: "text", step: 1, text });
  });
});

describe("会话消息", () => {
  it("三种块；省略 text 的空文本块补成空字符串；未知块类型被拒绝", () => {
    const base = { id: "00000000-0000-4000-8000-000000000001", title: "", mode: "assistant", model: "m", status: "idle", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T00:00:00Z" };
    const messageBase = { id: "00000000-0000-4000-8000-000000000002", sequence: 1, input_tokens: null, output_tokens: null, created_at: "2026-11-16T00:00:00Z", tool_calls: [] };
    const detail = decodeConversationDetail({
      ...base,
      messages: [{ ...messageBase, role: "assistant", content: [{ type: "text" }, { type: "tool_call", tool_call: { id: "c", name: "list_albums", arguments: {} } }] }],
    });
    expect(detail.messages[0]?.content).toEqual([{ type: "text", text: "" }, { type: "tool_call", tool_call: { id: "c", name: "list_albums", arguments: {} } }]);
    expect(() => decodeConversationDetail({ ...base, messages: [{ ...messageBase, role: "assistant", content: [{ type: "opaque" }] }] })).toThrow();
  });

  it("用户消息可以带页面上下文（C5），也可以是 null 或省略；字段缺失不算错", () => {
    const base = { id: "00000000-0000-4000-8000-000000000001", title: "", mode: "assistant", model: "m", status: "idle", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T00:00:00Z" };
    const messageBase = { sequence: 1, role: "user", content: [{ type: "text", text: "这是谁录的？" }], input_tokens: null, output_tokens: null, created_at: "2026-11-16T00:00:00Z", tool_calls: [] };
    const context = { page: { path: "/albums/x", kind: "album", title: "Bayreuth 1951" }, playback: { zone_id: "z", status: "playing" } };
    const detail = decodeConversationDetail({
      ...base,
      messages: [
        { ...messageBase, id: "00000000-0000-4000-8000-000000000002", context },
        { ...messageBase, id: "00000000-0000-4000-8000-000000000003", sequence: 2, context: null },
        { ...messageBase, id: "00000000-0000-4000-8000-000000000004", sequence: 3 },
      ],
    });
    expect(detail.messages.map((message) => message.context)).toEqual([context, null, undefined]);
    expect(() => decodeConversationDetail({ ...base, messages: [{ ...messageBase, id: "00000000-0000-4000-8000-000000000002", context: { page: { kind: "album" } } }] })).toThrow();
  });
});

describe("会话的模式与未处理的方案（C6）", () => {
  const base = { id: "00000000-0000-4000-8000-000000000001", title: "", model: "m", status: "idle", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T00:00:00Z", messages: [] };

  it("模式是 assistant、steward、operator 之一，其余被拒绝", () => {
    expect(decodeConversationDetail({ ...base, mode: "operator" }).mode).toBe("operator");
    expect(() => decodeConversationDetail({ ...base, mode: "autopilot" })).toThrow();
  });

  it("pending_plan 可以是方案、null 或省略；工具调用可以在等确认", () => {
    expect(decodeConversationDetail({ ...base, mode: "assistant", pending_plan: plan }).pending_plan).toEqual(plan);
    expect(decodeConversationDetail({ ...base, mode: "assistant", pending_plan: null }).pending_plan).toBeNull();
    expect(decodeConversationDetail({ ...base, mode: "assistant" }).pending_plan).toBeUndefined();
    const record = { id: "00000000-0000-4000-8000-0000000000aa", name: "correct_release", input: {}, output: null, truncated: false, untrusted: true, status: "awaiting_confirmation", error_code: null, duration_ms: null };
    const message = { id: "00000000-0000-4000-8000-000000000002", sequence: 1, role: "assistant", content: [], input_tokens: null, output_tokens: null, created_at: "2026-11-16T00:00:00Z", tool_calls: [record] };
    expect(decodeConversationDetail({ ...base, mode: "assistant", messages: [message] }).messages[0]?.tool_calls[0]?.status).toBe("awaiting_confirmation");
  });

  it("改会话的响应可以带模式，也可以不带", () => {
    expect(decodeConversationPatch({ title: "t", model: "", mode: "operator" }).mode).toBe("operator");
    expect(decodeConversationPatch({ title: "t", model: "" }).mode).toBeUndefined();
  });
});

describe("审查（C7）", () => {
  const review = {
    review_id: "00000000-0000-4000-8000-0000000000e1",
    plan_id: plan.plan_id,
    model: "review-model",
    status: "completed",
    policy_version: 1,
    expires_at: "2026-11-16T00:15:00Z",
    items: [{ item_id: planItem.id, decision: "needs_human_confirmation", risk: "high", reasons: ["不可撤销的修改"], source: "policy" }],
  };

  it("review_started 与 review_finished：step 可以省略；不可用的审查没有项；未知的结论被拒绝", () => {
    expect(decodeRunEvent("review_started", JSON.stringify({ type: "review_started", step: 1, plan_id: plan.plan_id, model: "review-model" }))).toMatchObject({ plan_id: plan.plan_id, model: "review-model" });
    expect(decodeRunEvent("review_finished", JSON.stringify({ type: "review_finished", review }))).toMatchObject({ review: { status: "completed" } });
    expect(decodeRunEvent("review_finished", JSON.stringify({ type: "review_finished", review: { ...review, status: "unavailable", items: [] } }))).toMatchObject({ review: { items: [] } });
    expect(() => decodeRunEvent("review_finished", JSON.stringify({ type: "review_finished", review: { ...review, items: [{ ...review.items[0], decision: "approve" }] } }))).toThrow();
    expect(decodeRunEvent("done", JSON.stringify({ type: "done", end: { reason: "review_unavailable" } }))).toMatchObject({ end: { reason: "review_unavailable" } });
  });

  it("方案带最近一次审查，方案项可以是 rejected_by_review", () => {
    const detail = { id: "00000000-0000-4000-8000-000000000001", title: "", mode: "steward", model: "m", status: "idle", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T00:00:00Z", messages: [] };
    const decoded = decodeConversationDetail({ ...detail, pending_plan: { ...plan, review, items: [{ ...planItem, decision: "rejected_by_review" }] }, plans: [{ ...plan, review: null }] });
    expect(decoded.pending_plan?.review?.items[0]?.source).toBe("policy");
    expect(decoded.pending_plan?.items[0]?.decision).toBe("rejected_by_review");
    expect(decoded.plans?.[0]?.review).toBeNull();
  });

  it("设置的审查模型与会话列表的等确认项数可以省略", () => {
    const settings = settingsOf();
    expect(decodeSettings({ ...settings, review_model: "review-model" }).review_model).toBe("review-model");
    const { review_model: _omitted, ...withoutReviewModel } = settings;
    expect(decodeSettings(withoutReviewModel).review_model).toBeUndefined();
    const item = { id: "00000000-0000-4000-8000-000000000001", title: "", mode: "steward", model: "", status: "idle", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T00:00:00Z" };
    const page = decodeConversationPage({ items: [{ ...item, pending_item_count: 2 }, item], pagination: { page: 1, page_size: 100, total: 2 } });
    expect(page.items.map((entry) => entry.pending_item_count)).toEqual([2, undefined]);
  });
});

describe("联网（C8）", () => {
  it("设置带搜索来源与 Tavily 密钥的末四位；接口类型可以是 responses；检测结论的自带搜索可以省略", () => {
    const settings = settingsOf({ search_source: "tavily", search_key_saved: true, search_key_last_four: "abcd", protocol: "responses", overrides: { native_search: "supported" } });
    const decoded = decodeSettings(settings);
    expect(decoded).toMatchObject({ search_source: "tavily", search_key_last_four: "abcd", protocol: "responses" });
    expect(effectiveNativeSearch(decoded)).toBe("supported");
    expect(effectiveNativeSearch(decodeSettings(settingsOf({ overrides: {} })))).toBe("unknown");
    const probed = decodeSettings(settingsOf({ probe_result: { models: [], tool_support: "supported", native_search: "unsupported", responses_available: true } }));
    expect(effectiveNativeSearch(probed)).toBe("unsupported");
    expect(probed.probe_result?.responses_available).toBe(true);
    const { search_source: _omitted, ...withoutSource } = settings;
    expect(() => decodeSettings(withoutSource)).toThrow();
    expect(() => decodeSettings({ ...settings, search_source: "brave" })).toThrow();
    expect(Object.keys(searchSettingsDefaults).every((key) => key in decoded)).toBe(true);
  });

  it("测试搜索的结果：成功带条数，失败带原因", () => {
    expect(decodeSearchTest({ ok: true, result_count: 3 })).toEqual({ ok: true, result_count: 3 });
    expect(decodeSearchTest({ ok: false, code: "searxng_json_disabled" })).toEqual({ ok: false, code: "searxng_json_disabled" });
    expect(() => decodeSearchTest({ ok: true })).toThrow();
    expect(() => decodeSearchTest({ code: "x" })).toThrow();
  });

  it("search 与 citations 事件；结果与标题可以省略", () => {
    expect(decodeRunEvent("search", JSON.stringify({ type: "search", step: 1, search_id: "s1", query: "贝九 1951", status: "completed", results: [{ title: "Bayreuth", url: "https://example.org/a" }] }))).toMatchObject({
      search_id: "s1",
      results: [{ title: "Bayreuth" }],
    });
    expect(decodeRunEvent("search", JSON.stringify({ type: "search", step: 1, search_id: "s2", query: "q", status: "failed", error_code: "max_uses_exceeded" }))).toMatchObject({ status: "failed", error_code: "max_uses_exceeded" });
    expect(() => decodeRunEvent("search", JSON.stringify({ type: "search", step: 1, search_id: "s3", query: "q", status: "running" }))).toThrow();
    expect(decodeRunEvent("citations", JSON.stringify({ type: "citations", step: 2, citations: [{ url: "https://example.org/a" }] }))).toEqual({ type: "citations", step: 2, citations: [{ url: "https://example.org/a" }] });
  });

  it("助手消息可以带 searches 与 citations，省略时照常解码", () => {
    const detail = { id: "00000000-0000-4000-8000-000000000001", title: "", mode: "assistant", model: "m", status: "idle", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T00:00:00Z" };
    const message = { id: "00000000-0000-4000-8000-000000000002", sequence: 1, role: "assistant", content: [{ type: "text", text: "答" }], input_tokens: null, output_tokens: null, created_at: "2026-11-16T00:00:00Z", tool_calls: [] };
    const decoded = decodeConversationDetail({
      ...detail,
      messages: [{ ...message, searches: [{ search_id: "s1", query: "q", status: "completed", results: [] }], citations: [{ url: "https://example.org", title: "" }] }, { ...message, id: "00000000-0000-4000-8000-000000000003", sequence: 2 }],
    });
    expect(decoded.messages[0]?.searches?.[0]?.query).toBe("q");
    expect(decoded.messages[0]?.citations).toEqual([{ url: "https://example.org", title: "" }]);
    expect(decoded.messages[1]?.searches).toBeUndefined();
  });
});

// C10 的新字段与新接口：手写的合成数据覆盖边界（缺字段、错类型、越界）；后端样本的完整解码在 src/test/api-contract.test.ts。
describe("C10：后台运行、未读与记忆", () => {
  const conversation = {
    id: "00000000-0000-4000-8000-000000000001",
    title: "",
    mode: "assistant",
    model: "m",
    status: "running",
    created_at: "2026-11-16T00:00:00Z",
    updated_at: "2026-11-16T00:00:00.123456Z",
    unread: false,
    run_status: "running",
    last_read_at: null,
    last_run_finished_at: "2026-11-15T23:00:00.000001Z",
  };

  it("会话列表与详情带未读、运行状态与两个时间；详情另带摘要分界；旧后端没有这些字段时照常解码", () => {
    const page = { items: [{ ...conversation, pending_item_count: 0 }], pagination: { page: 1, page_size: 100, total: 1 } };
    expect(decodeConversationPage(page)).toEqual(page);
    const detail = { ...conversation, messages: [], pending_plan: null, plans: [], summary_through_message_id: "00000000-0000-4000-8000-0000000000aa" };
    expect(decodeConversationDetail(detail)).toEqual(detail);
    expect(decodeConversationDetail({ ...detail, summary_through_message_id: null }).summary_through_message_id).toBeNull();
    const { unread: _unread, run_status: _status, last_read_at: _read, last_run_finished_at: _finished, ...old } = conversation;
    expect(decodeConversationPage({ ...page, items: [old] }).items[0]?.unread).toBeUndefined();
    expect(() => decodeConversationPage({ ...page, items: [{ ...conversation, run_status: "waiting" }] })).toThrow();
    expect(() => decodeConversationPage({ ...page, items: [{ ...conversation, unread: "yes" }] })).toThrow();
  });

  it("设置带未读会话数（非负整数）；C10 之前的后端没有这一项", () => {
    expect(decodeSettings(settingsOf({ unread_conversation_count: 3 })).unread_conversation_count).toBe(3);
    const { unread_conversation_count: _count, ...old } = settingsOf();
    expect(decodeSettings(old).unread_conversation_count).toBeUndefined();
    expect(() => decodeSettings(settingsOf({ unread_conversation_count: -1 }))).toThrow();
  });

  it("事件带 seq；history_gap 必须有 seq；history_compacted 带分界消息", () => {
    expect(decodeRunEvent("text", JSON.stringify({ type: "text", seq: 12, step: 1, text: "合并的文字" }))).toEqual({ type: "text", seq: 12, step: 1, text: "合并的文字" });
    expect(decodeRunEvent("done", JSON.stringify({ type: "done", seq: 13, end: { reason: "cancelled" } }))).toMatchObject({ seq: 13 });
    expect(decodeRunEvent("history_gap", JSON.stringify({ type: "history_gap", seq: 40 }))).toEqual({ type: "history_gap", seq: 40 });
    expect(() => decodeRunEvent("history_gap", JSON.stringify({ type: "history_gap" }))).toThrow();
    const compacted = { type: "history_compacted", seq: 2, through_message_id: "00000000-0000-4000-8000-0000000000aa" };
    expect(decodeRunEvent("history_compacted", JSON.stringify(compacted))).toEqual(compacted);
    expect(() => decodeRunEvent("text", JSON.stringify({ type: "text", seq: -1, step: 1, text: "x" }))).toThrow();
  });

  it("标记已读的响应", () => {
    expect(decodeMarkedRead({ read: true })).toEqual({ read: true });
    expect(() => decodeMarkedRead({ read: false })).toThrow();
  });

  it("记忆：列表带条数与字数上限；单条按原样解码", () => {
    const memory = { id: "00000000-0000-4000-8000-000000000801", content: "回答都用繁体", created_at: "2026-11-16T00:00:00Z", updated_at: "2026-11-16T00:00:01Z" };
    const list = { items: [memory], limit: 50, max_length: 500 };
    expect(decodeMemoryList(list)).toEqual(list);
    expect(decodeMemory(memory)).toEqual(memory);
    expect(decodeMemoryList({ items: [], limit: 50, max_length: 500 }).items).toEqual([]);
    expect(() => decodeMemoryList({ items: [], limit: 0, max_length: 500 })).toThrow();
    expect(() => decodeMemory({ ...memory, id: "x" })).toThrow();
  });
});
