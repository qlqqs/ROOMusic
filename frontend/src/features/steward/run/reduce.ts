import type { MessageDTO, RunEndDTO, RunEvent } from "../api/decoders";

// 事件 → 回合视图的纯函数。流式显示与刷新后从已落库消息还原用同一种结构，两者显示一致。

/** 过程里的一次工具调用。callId 是模型给的调用标识。 */
export type StepItem = {
  callId: string;
  name: string;
  input: unknown;
  status: "running" | "succeeded" | "failed";
  /** 未加来源标记的工具输出；失败时是 {code, message}，没有结果时是 null。 */
  output: unknown;
  errorCode: string | null;
  /** 执行耗时；流式事件里没有，落库后才有。 */
  durationMs: number | null;
  /**
   * 实际执行过：流式里收到的调用都执行过（没执行的不发事件）；落库的以有耗时为准
   * （因上限或取消没有执行的、中断的都没有耗时）。
   */
  executed: boolean;
};

/**
 * 一个回合：一条用户消息，加上到下一条用户消息之前的全部助手与工具消息。
 * items 是所有步骤的工具调用（按顺序），texts 是各步的文字（按步骤顺序，可能有空的）。
 */
export type Turn = { key: string; userText: string | null; items: StepItem[]; texts: string[] };

/**
 * 一次运行收到的事件归并出的样子；texts 的下标是 step - 1。
 * waiting：在等模型的下一段输出（刚开始，或这一步的工具都执行完了），界面显示“正在想…”。
 */
export type RunView = { items: StepItem[]; texts: string[]; end: RunEndDTO | null; waiting: boolean };

export const emptyRunView: RunView = { items: [], texts: [], end: null, waiting: true };

export function applyRunEvent(view: RunView, event: RunEvent): RunView {
  switch (event.type) {
    case "text": {
      const texts = [...view.texts];
      while (texts.length < event.step) texts.push("");
      texts[event.step - 1] = (texts[event.step - 1] ?? "") + event.text;
      return { ...view, texts, waiting: false };
    }
    case "tool_started": {
      const { call_id: callId, name, input } = event.tool_started;
      return { ...view, waiting: false, items: [...view.items, { callId, name, input, status: "running", output: null, errorCode: null, durationMs: null, executed: true }] };
    }
    case "tool_finished": {
      const finished = event.tool_finished;
      const items = view.items.map((item) =>
        item.callId === finished.call_id && item.status === "running"
          ? { ...item, status: finished.status, output: finished.output, errorCode: finished.error_code ?? null }
          : item,
      );
      return { ...view, items, waiting: !items.some((item) => item.status === "running") };
    }
    case "usage":
      return view;
    case "done":
      return { ...view, end: event.end, waiting: false };
  }
}

function textOf(message: MessageDTO): string {
  return message.content.map((block) => (block.type === "text" ? block.text : "")).join("");
}

/**
 * 已落库消息 → 回合。助手消息的 content 里 tool_call 块的顺序与 tool_calls 的顺序一致（合同 4.4），按下标对上：
 * 调用标识、名称、参数取自块，状态、输出、错误码与耗时取自存储的调用记录。工具消息的内容已经在调用记录里，不再单独显示。
 */
export function turnsFromMessages(messages: readonly MessageDTO[]): Turn[] {
  const turns: Turn[] = [];
  for (const message of messages) {
    if (message.role === "user") {
      turns.push({ key: message.id, userText: textOf(message), items: [], texts: [] });
      continue;
    }
    if (message.role !== "assistant") continue;
    let turn = turns.at(-1);
    if (turn === undefined) {
      turn = { key: message.id, userText: null, items: [], texts: [] };
      turns.push(turn);
    }
    turn.texts.push(textOf(message));
    let index = 0;
    for (const block of message.content) {
      if (block.type !== "tool_call") continue;
      const record = message.tool_calls[index];
      index += 1;
      turn.items.push({
        callId: block.tool_call.id,
        name: block.tool_call.name,
        input: block.tool_call.arguments,
        status: record?.status ?? "failed",
        output: record?.output ?? null,
        errorCode: record?.error_code ?? null,
        durationMs: record?.duration_ms ?? null,
        executed: (record?.duration_ms ?? null) !== null,
      });
    }
  }
  return turns;
}

/** 上一次运行没有给出最终回答：最后一条消息是用户或工具消息（可以用继续接口接着跑）。 */
export function isIncomplete(messages: readonly MessageDTO[]): boolean {
  const last = messages.at(-1);
  return last !== undefined && last.role !== "assistant";
}

/**
 * 过程摘要：查了几处（实际执行过的调用），用时（各条目耗时之和）。
 * 还没有耗时数据（运行刚结束、落库内容还没重读回来）时 durationMs 为 null，只写次数。
 */
export function summarizeSteps(items: readonly StepItem[]): { count: number; durationMs: number | null } {
  const executed = items.filter((item) => item.executed);
  const timed = executed.filter((item) => item.durationMs !== null);
  return { count: executed.length, durationMs: timed.length === 0 ? null : timed.reduce((sum, item) => sum + (item.durationMs ?? 0), 0) };
}

/** “用时”的文字：不到 1 秒、N 秒、N 分 M 秒。 */
export function formatElapsed(durationMs: number): string {
  if (durationMs < 1000) return "不到 1 秒";
  const seconds = Math.round(durationMs / 1000);
  if (seconds < 60) return `${seconds} 秒`;
  const minutes = Math.floor(seconds / 60);
  const rest = seconds % 60;
  return rest === 0 ? `${minutes} 分钟` : `${minutes} 分 ${rest} 秒`;
}
