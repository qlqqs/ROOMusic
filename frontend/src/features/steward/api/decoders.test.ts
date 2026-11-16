import { describe, expect, it } from "vitest";
import { decodeConversationDetail, decodeRunEvent } from "./decoders";

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
