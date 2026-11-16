import type { CitationDTO, ConversationDetailDTO, MessageDTO, RunEndDTO, RunEvent, SearchRecordDTO } from "../api/decoders";

// 事件 → 回合视图的纯函数。流式显示与刷新后从已落库消息还原用同一种结构，两者显示一致。

/**
 * 模型自带搜索（C8）在过程里的条目名：它不是登记给模型的工具，名字带 $，不会与工具重名。
 * 条目的 input 是 {q: 查询词}，output 是 {results: [{title, url}]}，与 web_search 工具的形状相近，过程条目按同样的规则显示。
 */
export const NATIVE_SEARCH = "$native_web_search";

/** 回答引用的一处来源（C8）：地址与标题，标题可能为空（显示时用域名）。都来自外部网页，只按文字显示。 */
export type Citation = { url: string; title: string };

/** 过程里的一次工具调用。callId 是模型给的调用标识。 */
export type StepItem = {
  callId: string;
  name: string;
  input: unknown;
  /** awaiting_confirmation：在方案里等用户确认，还没执行（C6）。 */
  status: "running" | "succeeded" | "failed" | "awaiting_confirmation";
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
  /** 回合里排在这次调用之前的文字段数（它所在那一步的文字也算）：方案卡片据此放在这一步的文字之后、后面的回答之前。 */
  textsBefore: number;
};

/**
 * 一个回合：一条用户消息，加上到下一条用户消息之前的全部助手与工具消息。
 * items 是所有步骤的工具调用（按顺序），texts 是各步的文字（按步骤顺序，可能有空的）。
 */
export type Turn = { key: string; userText: string | null; items: StepItem[]; texts: string[]; citations: Citation[] };

/**
 * 一次运行收到的事件归并出的样子；texts 的下标是 step - 1。
 * waiting：在等模型的下一段输出（刚开始，或这一步的工具都执行完了），界面显示“正在想…”。
 * citations：回答引用的来源（C8，各步合在一起、按地址去重）。
 * gap：接回时较早的事件已经省略（C10 的 history_gap），收到的内容不完整，界面改以落库内容为准，结束后再读一次。
 * compactedThrough：这次运行把较早的历史压成了摘要（C10 的 history_compacted），摘要覆盖到的最后一条消息。
 */
export type RunView = { items: StepItem[]; texts: string[]; citations: Citation[]; end: RunEndDTO | null; waiting: boolean; gap: boolean; compactedThrough: string | null };

export const emptyRunView: RunView = { items: [], texts: [], citations: [], end: null, waiting: true, gap: false, compactedThrough: null };

/**
 * 按事件序号去重（C10）：断线接回时带着收到的最后一个序号，服务端只回放它之后的内容；序号不大于它的事件已经收到过，丢掉。
 * 回放里合并过的文字序号是合并范围的末尾，服务端按序号给出准确的后缀，这里照常追加。没有序号的事件（旧后端）都收下。
 */
export function isNewEvent(lastSeq: number, event: RunEvent): boolean {
  return event.seq === undefined || event.seq > lastSeq;
}

/** 一次模型自带搜索的条目：搜索事件到达时已经结束，成功或失败；不计耗时。 */
function searchItemOf(search: SearchRecordDTO, textsBefore: number): StepItem {
  const results = (search.results ?? []).map((result) => ({ title: result.title ?? "", url: result.url }));
  const failed = search.status === "failed";
  return {
    callId: search.search_id,
    name: NATIVE_SEARCH,
    input: { q: search.query },
    status: failed ? "failed" : "succeeded",
    output: { results },
    errorCode: failed ? (search.error_code ?? null) : null,
    durationMs: null,
    executed: true,
    textsBefore,
  };
}

/** 合并来源：按地址去重，先出现的在前。 */
export function mergeCitations(current: readonly Citation[], incoming: readonly CitationDTO[]): Citation[] {
  const seen = new Set(current.map((citation) => citation.url));
  const merged = [...current];
  for (const citation of incoming) {
    if (seen.has(citation.url)) continue;
    seen.add(citation.url);
    merged.push({ url: citation.url, title: citation.title ?? "" });
  }
  return merged;
}

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
      // 确认后执行的方案项（C6），或审查批准后执行的项（C7，审查前已经为它发过 tool_started）：已经在列表里，原地换成执行中。
      if (view.items.some((item) => item.callId === callId && (item.status === "awaiting_confirmation" || item.status === "running"))) {
        const items = view.items.map((item) => (item.callId === callId ? { ...item, input, status: "running" as const, executed: true } : item));
        return { ...view, waiting: false, items };
      }
      return { ...view, waiting: false, items: [...view.items, { callId, name, input, status: "running", output: null, errorCode: null, durationMs: null, executed: true, textsBefore: event.step ?? view.texts.length }] };
    }
    case "tool_finished": {
      const finished = event.tool_finished;
      const items = view.items.map((item) =>
        item.callId === finished.call_id && (item.status === "running" || item.status === "awaiting_confirmation")
          ? { ...item, status: finished.status, output: finished.output, errorCode: finished.error_code ?? null, executed: item.executed || finished.status === "succeeded" }
          : item,
      );
      return { ...view, items, waiting: !items.some((item) => item.status === "running") };
    }
    case "plan": {
      // 方案里的调用没有执行，作为等待确认的条目放进过程；方案本身由 run-store 交给方案的 store。
      const textsBefore = event.step ?? view.texts.length;
      const known = new Set(view.items.map((item) => item.callId));
      // 后端先为这些调用发过 tool_started：原地改成等待确认、没有执行。
      const items = view.items.map((item) =>
        event.plan.items.some((entry) => entry.call_id === item.callId) ? { ...item, status: "awaiting_confirmation" as const, executed: false } : item,
      );
      for (const entry of event.plan.items) {
        if (known.has(entry.call_id)) continue;
        items.push({ callId: entry.call_id, name: entry.tool, input: null, status: "awaiting_confirmation", output: null, errorCode: null, durationMs: null, executed: false, textsBefore });
      }
      return { ...view, items, waiting: false };
    }
    case "review_started":
      // 审查由方案或审查的区块显示“审查中”，这里不再显示“正在想…”。
      return { ...view, waiting: false };
    case "review_finished":
      // 审查之后：批准的项接着执行，或模型接着回答。
      return { ...view, waiting: !view.items.some((item) => item.status === "running") };
    case "search": {
      // 同一次搜索再来一次事件时原地替换。
      const item = searchItemOf(event, event.step ?? view.texts.length);
      const known = view.items.some((entry) => entry.callId === item.callId && entry.name === NATIVE_SEARCH);
      return { ...view, waiting: false, items: known ? view.items.map((entry) => (entry.callId === item.callId && entry.name === NATIVE_SEARCH ? item : entry)) : [...view.items, item] };
    }
    case "citations":
      return { ...view, citations: mergeCitations(view.citations, event.citations) };
    case "usage":
      return view;
    case "history_gap":
      return { ...view, gap: true };
    case "history_compacted":
      return { ...view, compactedThrough: event.through_message_id };
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
 * 助手消息上的模型自带搜索（searches）与引用的来源（citations）同样还原成条目与回合的来源，与流式显示一致。
 */
export function turnsFromMessages(messages: readonly MessageDTO[]): Turn[] {
  const turns: Turn[] = [];
  for (const message of messages) {
    if (message.role === "user") {
      turns.push({ key: message.id, userText: textOf(message), items: [], texts: [], citations: [] });
      continue;
    }
    if (message.role !== "assistant") continue;
    let turn = turns.at(-1);
    if (turn === undefined) {
      turn = { key: message.id, userText: null, items: [], texts: [], citations: [] };
      turns.push(turn);
    }
    turn.texts.push(textOf(message));
    // 模型自带搜索（C8）发生在这一步生成的过程中，排在这一步的工具调用之前；来源合进回合，按地址去重。
    for (const search of message.searches ?? []) turn.items.push(searchItemOf(search, turn.texts.length));
    turn.citations = mergeCitations(turn.citations, message.citations ?? []);
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
        textsBefore: turn.texts.length,
      });
    }
  }
  return turns;
}

/** 时间戳的先后：Date.parse 只精确到毫秒，后端的时间到微秒，毫秒相同时再比较小数秒的其余位。 */
export function compareTimestamps(a: string, b: string): number {
  const byMs = Date.parse(a) - Date.parse(b);
  if (byMs !== 0) return byMs;
  const fraction = (value: string) => (/\.(\d+)/.exec(value)?.[1] ?? "").padEnd(9, "0").slice(3, 9);
  return fraction(a).localeCompare(fraction(b));
}

/**
 * 接回一次不是本页发起的运行（刷新页面、另一个标签页、重新打开会话，C10）：已落库的消息里哪些属于这次运行。
 * 这次运行的消息都落在上一次运行结束（last_run_finished_at）之后；接回从头回放，会把它们再显示一遍，所以只取之前的部分。
 * 发消息开始的运行，第一条是用户这次说的话：它留在已落库的部分，回放的内容并进它所在的回合。
 * 返回的是要保留的最后一条消息的序号。
 */
export function runBaseSequenceOf(detail: Pick<ConversationDetailDTO, "messages" | "last_run_finished_at">): number {
  const finished = detail.last_run_finished_at ?? null;
  const first = detail.messages.findIndex((message) => finished === null || compareTimestamps(message.created_at, finished) > 0);
  if (first < 0) return detail.messages.at(-1)?.sequence ?? 0;
  const head = detail.messages[first];
  if (head?.role === "user") return head.sequence;
  return detail.messages[first - 1]?.sequence ?? 0;
}

/**
 * 摘要分界（C10）所在的回合：摘要覆盖到的那条消息属于哪个回合，返回回合的 key（与 turnsFromMessages 一致）；
 * 找不到这条消息时是 null。分界总落在回合的边界上，界面在这个回合之后标一行“更早的内容已压缩”。
 */
export function compactedTurnKeyOf(messages: readonly MessageDTO[], throughMessageId: string): string | null {
  let key: string | null = null;
  for (const message of messages) {
    if (message.role === "user" || (key === null && message.role === "assistant")) key = message.id;
    if (message.id === throughMessageId) return key;
  }
  return null;
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
