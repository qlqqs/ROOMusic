import { describe, expect, it } from "vitest";
import type { ConversationDetailDTO, MessageDTO } from "../api/decoders";
import { emptyRunView, runBaseSequenceOf } from "../run/reduce";
import type { ActiveRun, RunSnapshot } from "../run/run-store";
import { threadOf } from "./use-run";

const id = "00000000-0000-4000-8000-000000000001";
const finished = "2026-11-16T00:00:10Z";

function message(sequence: number, role: MessageDTO["role"], text: string, createdAt: string): MessageDTO {
  return {
    id: `00000000-0000-4000-8000-${String(sequence).padStart(12, "0")}`,
    sequence,
    role,
    content: [{ type: "text", text }],
    input_tokens: null,
    output_tokens: null,
    created_at: createdAt,
    tool_calls: [],
  };
}

/** 刷新时会话还在运行：上一次运行（1、2）之后，这次运行的用户消息（3）与已经落库的第一步（4）。 */
const detail: ConversationDetailDTO = {
  id,
  title: "",
  mode: "assistant",
  model: "",
  status: "running",
  created_at: "2026-11-16T00:00:00Z",
  updated_at: "2026-11-16T00:00:20Z",
  last_run_finished_at: finished,
  messages: [
    message(1, "user", "上一个问题", "2026-11-16T00:00:01Z"),
    message(2, "assistant", "上一个回答", "2026-11-16T00:00:02Z"),
    message(3, "user", "这次的问题", "2026-11-16T00:00:11Z"),
    message(4, "assistant", "第一步说的话", "2026-11-16T00:00:12Z"),
  ],
  pending_plan: null,
};

function snapshotWith(run: Partial<ActiveRun>): RunSnapshot {
  const active: ActiveRun = { conversationId: id, userText: null, baseSequence: runBaseSequenceOf(detail), view: emptyRunView, phase: "running", lastSeq: 0, reconnecting: false, ...run };
  return { runs: new Map([[id, active]]), ends: new Map(), detached: new Map() };
}

describe("接回时的对话内容（C10）", () => {
  it("回放的内容并进这次的用户消息所在回合，已经落库的这次运行的部分不重复显示", () => {
    const view = threadOf(detail, snapshotWith({ view: { ...emptyRunView, texts: ["第一步说的话", "最终回答"], waiting: false } }), id);
    expect(view.turns.map((turn) => [turn.userText, turn.texts])).toEqual([
      ["上一个问题", ["上一个回答"]],
      ["这次的问题", ["第一步说的话", "最终回答"]],
    ]);
    expect(view.live).toBe(true);
  });

  it("较早的事件已经省略（history_gap）：显示全部落库内容，运行只给出进行中的状态", () => {
    const view = threadOf(detail, snapshotWith({ view: { ...emptyRunView, texts: ["", "后半段"], waiting: false, gap: true } }), id);
    expect(view.turns.map((turn) => turn.texts)).toEqual([["上一个回答"], ["第一步说的话"]]);
    expect(view.waiting).toBe(true);
  });

  it("摘要分界：取会话详情的 summary_through_message_id，运行里的 history_compacted 优先", () => {
    const idle = { ...detail, status: "idle" as const, summary_through_message_id: detail.messages[1]?.id ?? null };
    const empty: RunSnapshot = { runs: new Map(), ends: new Map(), detached: new Map() };
    const stored = threadOf(idle, empty, id);
    expect(stored.compactedAfter).toBe(stored.turns[0]?.key);
    expect(threadOf({ ...idle, summary_through_message_id: null }, empty, id).compactedAfter).toBeNull();
    const live = threadOf(detail, snapshotWith({ view: { ...emptyRunView, compactedThrough: detail.messages[3]?.id ?? null } }), id);
    expect(live.compactedAfter).toBe(detail.messages[2]?.id);
  });
});
