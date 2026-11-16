import { failureOf, type ApiFailure } from "../../../shared/api";
import { SseParser } from "../../../shared/lib/sse";
import { decodeRunEvent, type RunEndDTO } from "../api/decoders";
import { cancelConversation, openRun } from "../api/endpoints";
import type { StewardRequestContext } from "../context/context";
import { applyRunEvent, emptyRunView, type RunView } from "./reduce";

// 进行中的运行（应用级）：发消息或继续时由这里发起请求并读流，与组件的生命周期无关，
// 切页面、面板与整页之间切换都不打断（R6）。按会话 ID 存；登出或换用户时由应用调用 resetRuns 中止并清空。

/** 一次运行怎么结束的。done 是服务端的 done 事件；disconnected 是流没有 done 就断了；request_failed 是开始流之前就被拒绝或连不上。 */
export type RunEnd =
  | { kind: "done"; end: RunEndDTO }
  | { kind: "disconnected" }
  | { kind: "request_failed"; failure: ApiFailure };

export type ActiveRun = {
  conversationId: string;
  /** 发消息时是用户这次说的话；继续运行时是 null（不追加用户消息）。 */
  userText: string | null;
  /** 开始时已落库的最后一条消息的序号：运行期间重读会话时，只取这之前的消息，之后的由运行自己显示，不重复。 */
  baseSequence: number;
  view: RunView;
  /** running：在读流；stopping：已请求停止；settling：已结束，正在重读会话，读完后移除。 */
  phase: "running" | "stopping" | "settling";
};

export type RunSnapshot = {
  runs: ReadonlyMap<string, ActiveRun>;
  /** 每个会话最近一次运行的结束信息，下一次运行开始时清掉；刷新页面后没有（从落库内容判断）。 */
  ends: ReadonlyMap<string, RunEnd>;
};

export type StartRunParams = {
  conversationId: string;
  text: string | null;
  baseSequence: number;
  /** 随这条消息发的页面上下文；null 时请求体里没有。继续（text 为 null）不带上下文，这里也是 null。 */
  context: StewardRequestContext | null;
  /** 运行结束后重读会话与列表（以落库内容为准）；完成后这次运行从 store 里移除。 */
  refresh: () => Promise<unknown>;
  /** 一次工具调用结束（成功或失败）时，带上工具名：调用方据此刷新被改动的个人数据。 */
  onToolFinished: (name: string) => void;
};

/** 流是否开起来了：没开起来时带失败原因（调用方据此把草稿放回输入框）；同一会话已有运行时 failure 为 null。 */
export type StartResult = { started: true } | { started: false; failure: ApiFailure | null };

let snapshot: RunSnapshot = { runs: new Map(), ends: new Map() };
const controllers = new Map<string, AbortController>();
const listeners = new Set<() => void>();
/** resetRuns 之后递增：旧运行的回调据此不再写入。 */
let epoch = 0;

function publish(next: RunSnapshot) {
  snapshot = next;
  for (const listener of listeners) listener();
}

function setRun(id: string, update: ((run: ActiveRun) => ActiveRun) | null) {
  const runs = new Map(snapshot.runs);
  const current = runs.get(id);
  if (current === undefined) return;
  if (update === null) runs.delete(id);
  else runs.set(id, update(current));
  publish({ ...snapshot, runs });
}

function setEnd(id: string, end: RunEnd | null) {
  const ends = new Map(snapshot.ends);
  if (end === null) ends.delete(id);
  else ends.set(id, end);
  publish({ ...snapshot, ends });
}

export function subscribeRuns(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function getRunSnapshot(): RunSnapshot {
  return snapshot;
}

export function startRun({ conversationId, text, baseSequence, context, refresh, onToolFinished }: StartRunParams): Promise<StartResult> {
  if (snapshot.runs.has(conversationId)) return Promise.resolve({ started: false, failure: null });
  const controller = new AbortController();
  controllers.set(conversationId, controller);
  const runs = new Map(snapshot.runs);
  runs.set(conversationId, { conversationId, userText: text, baseSequence, view: emptyRunView, phase: "running" });
  const ends = new Map(snapshot.ends);
  ends.delete(conversationId);
  publish({ runs, ends });

  const current = epoch;
  const live = () => current === epoch;
  return new Promise((resolve) => {
    void (async () => {
      let response: Response;
      try {
        response = await openRun(conversationId, text === null ? null : { text, context }, controller.signal);
      } catch (error) {
        if (!live()) return resolve({ started: false, failure: null });
        controllers.delete(conversationId);
        const failure = failureOf(error) ?? { kind: "network" as const, requestId: "" };
        setRun(conversationId, null);
        if (failure.kind !== "aborted") setEnd(conversationId, { kind: "request_failed", failure });
        resolve({ started: false, failure });
        void refresh().catch(() => undefined);
        return;
      }
      resolve({ started: true });
      const end = await readStream(conversationId, response, controller.signal, live, onToolFinished);
      if (!live()) return;
      controllers.delete(conversationId);
      setRun(conversationId, (run) => ({ ...run, phase: "settling" }));
      setEnd(conversationId, end);
      await refresh().catch(() => undefined);
      if (live()) setRun(conversationId, null);
    })();
  });
}

async function readStream(
  conversationId: string,
  response: Response,
  signal: AbortSignal,
  live: () => boolean,
  onToolFinished: (name: string) => void,
): Promise<RunEnd> {
  const body = response.body;
  if (body === null) return { kind: "disconnected" };
  const reader = body.getReader();
  const decoder = new TextDecoder();
  const parser = new SseParser();
  let end: RunEnd | null = null;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done || !live()) break;
      for (const message of parser.push(decoder.decode(value, { stream: true }))) {
        let event: ReturnType<typeof decodeRunEvent>;
        try {
          event = decodeRunEvent(message.event, message.data);
        } catch {
          // 解不了的事件丢掉：结束后会重读会话，以落库内容为准。
          continue;
        }
        if (event === null) continue;
        if (event.type === "done") end = { kind: "done", end: event.end };
        const applied = event;
        setRun(conversationId, (run) => ({ ...run, view: applyRunEvent(run.view, applied) }));
        if (event.type === "tool_finished") {
          const callId = event.tool_finished.call_id;
          const name = snapshot.runs.get(conversationId)?.view.items.find((item) => item.callId === callId)?.name;
          if (name !== undefined) onToolFinished(name);
        }
      }
    }
  } catch {
    // 读流时断开（网络、被中止）：按下面的规则判断结果。
  } finally {
    reader.releaseLock();
  }
  if (end !== null) return end;
  // 停止时请求了取消却没等到 done（取消接口失败、只能断开），也算已停止。
  if (signal.aborted) return { kind: "done", end: { reason: "cancelled" } };
  return { kind: "disconnected" };
}

/**
 * 停止：先调用取消接口，服务端结束运行后照常发 done（此时会话已释放，重读到的是空闲状态）；
 * 取消接口失败或说本进程里没有这次运行时，直接断开连接（服务端在断开时自行取消）。
 */
export function stopRun(conversationId: string): void {
  const controller = controllers.get(conversationId);
  const run = snapshot.runs.get(conversationId);
  // 不是本页发起的运行（另一个标签页在跑）：取消接口按用户与会话取消，结果由轮询看到。
  if (controller === undefined || run === undefined) {
    void cancelConversation(conversationId).catch(() => undefined);
    return;
  }
  if (run.phase !== "running") return;
  setRun(conversationId, (current) => ({ ...current, phase: "stopping" }));
  cancelConversation(conversationId).then(
    (response) => {
      if (!response.data.cancelled) controller.abort();
    },
    () => controller.abort(),
  );
}

/** 登出或换用户：中止所有运行并清空。 */
export function resetRuns(): void {
  epoch += 1;
  for (const controller of controllers.values()) controller.abort();
  controllers.clear();
  publish({ runs: new Map(), ends: new Map() });
}
