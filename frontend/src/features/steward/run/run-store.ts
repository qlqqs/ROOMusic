import { failureOf, type ApiFailure } from "../../../shared/api";
import { SseParser } from "../../../shared/lib/sse";
import { decodeRunEvent, type PlanDTO, type RunEndDTO, type RunEvent } from "../api/decoders";
import { cancelConversation, openDecision, openEvents, openReview, openRun, type PlanDecisionBody } from "../api/endpoints";
import type { StewardRequestContext } from "../context/context";
import { applyRunEvent, emptyRunView, isNewEvent, type RunView, type StepItem } from "./reduce";

// 进行中的运行（应用级）：发消息、继续、确认方案时由这里发起请求并读流，打开正在运行的会话时由这里接回（C10），
// 与组件的生命周期无关，切页面、面板与整页之间切换都不打断（R6）。按会话 ID 存；登出或换用户时由应用调用 resetRuns 中止并清空。
// 运行在服务端不随连接断开而停止（C10）：流没有 done 就断了时带着收到的最后一个事件序号重新接回，按序号去重；
// 停止只走取消接口，服务端结束运行后每个接着的连接都收到同一个 done。

/**
 * 一次运行怎么结束的。done 是服务端的 done 事件；disconnected 是流断了、重新接回也没有接上（重试用完或被拒绝）；
 * request_failed 是开始流之前就被拒绝或连不上。
 */
export type RunEnd =
  | { kind: "done"; end: RunEndDTO }
  | { kind: "disconnected" }
  | { kind: "request_failed"; failure: ApiFailure };

export type ActiveRun = {
  conversationId: string;
  /** 发消息时是用户这次说的话；继续、确认方案与接回时是 null（不追加用户消息，并进最后一个回合）。 */
  userText: string | null;
  /** 开始时已落库的最后一条消息的序号：运行期间重读会话时，只取这之前的消息，之后的由运行自己显示，不重复。 */
  baseSequence: number;
  view: RunView;
  /** running：在读流；stopping：已请求停止；settling：已结束，正在重读会话，读完后移除。 */
  phase: "running" | "stopping" | "settling";
  /** 收到的最后一个事件序号（C10）：重新接回时带上，服务端只回放它之后的内容。 */
  lastSeq: number;
  /** 流断了，正在重新接回。 */
  reconnecting: boolean;
};

export type RunSnapshot = {
  runs: ReadonlyMap<string, ActiveRun>;
  /** 每个会话最近一次运行的结束信息，下一次运行开始时清掉；刷新页面后没有（从落库内容判断）。 */
  ends: ReadonlyMap<string, RunEnd>;
  /**
   * 接不回来的运行：会话 ID → 接回时会话的 updated_at（服务端没有这次运行的事件、重试用完或被拒绝）。
   * 会话详情还是这个版本时不再自动接回，免得反复请求；开始新的运行时清掉。
   */
  detached: ReadonlyMap<string, string>;
};

type RunCallbacks = {
  /** 运行结束后重读会话与列表（以落库内容为准）；完成后这次运行从 store 里移除。 */
  refresh: () => Promise<unknown>;
  /** 一次工具调用结束（成功或失败）时，带上工具名：调用方据此刷新被改动的个人数据。 */
  onToolFinished: (name: string) => void;
  /** 收到修改方案（C6）：调用方把它记进方案的 store。 */
  onPlan?: (plan: PlanDTO) => void;
  /** 收到审查的开始或结论（C7）：调用方把它记进审查的 store。 */
  onReview?: (event: Extract<RunEvent, { type: "review_started" | "review_finished" }>) => void;
  /** 接回时较早的事件已经省略（C10 的 history_gap）：调用方重读会话详情，以落库内容为准。 */
  onGap?: () => void;
  /**
   * 流结束了（done 不论原因、重新接回也没接上），在重读会话之前调用：审查中被取消或保存失败时不会有 review_finished，
   * 调用方据此清掉“审查中”（C7）。
   */
  onEnd?: () => void;
};

export type StartRunParams = RunCallbacks & {
  conversationId: string;
  text: string | null;
  baseSequence: number;
  /** 随这条消息发的页面上下文；null 时请求体里没有。继续（text 为 null）不带上下文，这里也是 null。 */
  context: StewardRequestContext | null;
  /**
   * 对一份方案的决定（C6）：走确认接口，text 为 null，并进最后一个回合。seed 是方案里等待确认的条目，
   * 执行结果按调用标识落在它们身上。开流前被拒绝时不记结束信息，由方案卡片就地说明。
   */
  decision?: { planId: string; body: PlanDecisionBody; seed: readonly StepItem[] };
  /** 重新审查一份方案（C7）：走审查接口，其余与 decision 相同（text 为 null，并进最后一个回合，开流前被拒绝时不记结束信息）。 */
  review?: { planId: string; seed: readonly StepItem[] };
};

export type AttachRunParams = RunCallbacks & {
  conversationId: string;
  /** 已落库的消息里保留到哪一条（runBaseSequenceOf）：之后的属于这次运行，由回放重新显示。 */
  baseSequence: number;
  /** 会话详情的 updated_at：接不回来时记下，同一版本不再自动接回。 */
  marker: string;
};

/** 流是否开起来了：没开起来时带失败原因（调用方据此把草稿放回输入框）；同一会话已有运行时 failure 为 null。 */
export type StartResult = { started: true } | { started: false; failure: ApiFailure | null };

export type RunStore = {
  subscribe: (listener: () => void) => () => void;
  getSnapshot: () => RunSnapshot;
  startRun: (params: StartRunParams) => Promise<StartResult>;
  /** 接回一次不是本页发起的运行；已有这次运行或它已经接不回来时返回 false。 */
  attachRun: (params: AttachRunParams) => boolean;
  stopRun: (conversationId: string) => void;
  reset: () => void;
};

/**
 * 断线后重新接回之前等多久：第一次马上，之后逐步拉长；用完还没接上就以落库内容为准，提示可以刷新页面。
 * 收到新事件后从头算。
 */
const RECONNECT_DELAYS: readonly number[] = [0, 1_000, 2_000, 4_000, 8_000, 15_000];

function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const timer = setTimeout(resolve, ms);
    signal.addEventListener(
      "abort",
      () => {
        clearTimeout(timer);
        resolve();
      },
      { once: true },
    );
  });
}

/** 打开接回的流：成功时是响应（204 表示没有这次运行的事件）；4xx 不再重试（会话被删、管家被关闭、登录失效）；其余稍后重试。 */
async function reopen(conversationId: string, after: number, signal: AbortSignal): Promise<Response | "retry" | "refused"> {
  try {
    return await openEvents(conversationId, after, signal);
  } catch (error) {
    const failure = failureOf(error);
    return failure?.kind === "http" && failure.status < 500 ? "refused" : "retry";
  }
}

/** 一个运行的 store。应用用下面导出的那一个；测试另建实例来模拟两个标签页。 */
export function createRunStore({ reconnectDelays = RECONNECT_DELAYS }: { reconnectDelays?: readonly number[] } = {}): RunStore {
  let snapshot: RunSnapshot = { runs: new Map(), ends: new Map(), detached: new Map() };
  const controllers = new Map<string, AbortController>();
  const listeners = new Set<() => void>();
  /** reset 之后递增：旧运行的回调据此不再写入。 */
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

  function setEnd(id: string, end: RunEnd) {
    const ends = new Map(snapshot.ends);
    ends.set(id, end);
    publish({ ...snapshot, ends });
  }

  function setDetached(id: string, marker: string) {
    const detached = new Map(snapshot.detached);
    detached.set(id, marker);
    publish({ ...snapshot, detached });
  }

  /** 登记一次运行：清掉这个会话上一次的结束信息与“接不回来”的记录。 */
  function begin(run: ActiveRun): { controller: AbortController; live: () => boolean } {
    const controller = new AbortController();
    controllers.set(run.conversationId, controller);
    const runs = new Map(snapshot.runs);
    runs.set(run.conversationId, run);
    const ends = new Map(snapshot.ends);
    ends.delete(run.conversationId);
    const detached = new Map(snapshot.detached);
    detached.delete(run.conversationId);
    publish({ runs, ends, detached });
    const current = epoch;
    return { controller, live: () => current === epoch };
  }

  /** 读一条流直到结束：返回 done 事件的结束信息（没有 done 就断了时是 null），以及这次有没有收到新事件。 */
  async function readStream(conversationId: string, response: Response, live: () => boolean, callbacks: RunCallbacks): Promise<{ done: RunEndDTO | null; received: boolean }> {
    const body = response.body;
    if (body === null) return { done: null, received: false };
    const reader = body.getReader();
    const decoder = new TextDecoder();
    const parser = new SseParser();
    let done: RunEndDTO | null = null;
    let received = false;
    try {
      for (;;) {
        const chunk = await reader.read();
        if (chunk.done || !live()) break;
        for (const message of parser.push(decoder.decode(chunk.value, { stream: true }))) {
          let event: ReturnType<typeof decodeRunEvent>;
          try {
            event = decodeRunEvent(message.event, message.data);
          } catch {
            // 解不了的事件丢掉：结束后会重读会话，以落库内容为准。
            continue;
          }
          if (event === null) continue;
          const run = snapshot.runs.get(conversationId);
          if (run === undefined || !isNewEvent(run.lastSeq, event)) continue;
          received = true;
          const applied = event;
          setRun(conversationId, (current) => ({ ...current, view: applyRunEvent(current.view, applied), lastSeq: applied.seq ?? current.lastSeq, reconnecting: false }));
          if (event.type === "done") done = event.end;
          if (event.type === "plan") callbacks.onPlan?.(event.plan);
          if (event.type === "review_started" || event.type === "review_finished") callbacks.onReview?.(event);
          if (event.type === "history_gap") callbacks.onGap?.();
          if (event.type === "tool_finished") {
            const callId = event.tool_finished.call_id;
            const name = snapshot.runs.get(conversationId)?.view.items.find((item) => item.callId === callId)?.name;
            if (name !== undefined) callbacks.onToolFinished(name);
          }
        }
      }
    } catch {
      // 读流时断开（网络、被中止）：没有 done 就重新接回。
    } finally {
      reader.releaseLock();
    }
    return { done, received };
  }

  async function finish(conversationId: string, end: RunEnd | null, live: () => boolean, callbacks: RunCallbacks) {
    callbacks.onEnd?.();
    controllers.delete(conversationId);
    setRun(conversationId, (run) => ({ ...run, phase: "settling", reconnecting: false }));
    if (end !== null) setEnd(conversationId, end);
    await callbacks.refresh().catch(() => undefined);
    if (live()) setRun(conversationId, null);
  }

  /**
   * 跟着一次运行直到结束。first 是已经打开的流；null 时先打开接回的流。流没有 done 就断了时按 reconnectDelays 重新接回：
   * 204（服务端没有这次运行的事件）、被拒绝或重试用完时不再接，以重读的落库内容为准；marker 不为 null 时记下接不回来。
   */
  async function follow(conversationId: string, first: Response | null, signal: AbortSignal, live: () => boolean, callbacks: RunCallbacks, marker: string | null) {
    let current: Response | null = first;
    let failures = 0;
    let end: RunEnd | null = null;
    let detach = false;
    for (;;) {
      if (current === null) {
        const opened = await reopen(conversationId, snapshot.runs.get(conversationId)?.lastSeq ?? 0, signal);
        if (!live()) return;
        if (opened === "refused") {
          end = { kind: "disconnected" };
          detach = true;
          break;
        }
        if (opened !== "retry") current = opened;
      }
      if (current !== null) {
        if (current.status === 204) {
          detach = true;
          break;
        }
        const result = await readStream(conversationId, current, live, callbacks);
        if (!live()) return;
        if (result.done !== null) {
          end = { kind: "done", end: result.done };
          break;
        }
        if (result.received) failures = 0;
        current = null;
      }
      const delay = reconnectDelays[failures];
      if (delay === undefined) {
        end = { kind: "disconnected" };
        detach = true;
        break;
      }
      failures += 1;
      setRun(conversationId, (run) => (run.reconnecting ? run : { ...run, reconnecting: true }));
      await sleep(delay, signal);
      if (!live()) return;
    }
    if (detach && marker !== null) setDetached(conversationId, marker);
    await finish(conversationId, end, live, callbacks);
  }

  function startRun({ conversationId, text, baseSequence, context, decision, review, ...callbacks }: StartRunParams): Promise<StartResult> {
    if (snapshot.runs.has(conversationId)) return Promise.resolve({ started: false, failure: null });
    const seed = decision?.seed ?? review?.seed;
    const view = seed === undefined ? emptyRunView : { ...emptyRunView, items: [...seed] };
    const { controller, live } = begin({ conversationId, userText: text, baseSequence, view, phase: "running", lastSeq: 0, reconnecting: false });
    return new Promise((resolve) => {
      void (async () => {
        let response: Response;
        try {
          response =
            decision !== undefined
              ? await openDecision(conversationId, decision.planId, decision.body, controller.signal)
              : review !== undefined
                ? await openReview(conversationId, review.planId, controller.signal)
                : await openRun(conversationId, text === null ? null : { text, context }, controller.signal);
        } catch (error) {
          if (!live()) return resolve({ started: false, failure: null });
          controllers.delete(conversationId);
          const failure = failureOf(error) ?? { kind: "network" as const, requestId: "" };
          setRun(conversationId, null);
          if (failure.kind !== "aborted" && seed === undefined) setEnd(conversationId, { kind: "request_failed", failure });
          resolve({ started: false, failure });
          void callbacks.refresh().catch(() => undefined);
          return;
        }
        resolve({ started: true });
        await follow(conversationId, response, controller.signal, live, callbacks, null);
      })();
    });
  }

  function attachRun({ conversationId, baseSequence, marker, ...callbacks }: AttachRunParams): boolean {
    if (snapshot.runs.has(conversationId) || snapshot.detached.get(conversationId) === marker) return false;
    const { controller, live } = begin({ conversationId, userText: null, baseSequence, view: emptyRunView, phase: "running", lastSeq: 0, reconnecting: false });
    void follow(conversationId, null, controller.signal, live, callbacks, marker);
    return true;
  }

  /**
   * 停止：调用取消接口（任何标签页发起的运行都可以），服务端结束运行后每个接着的连接都收到 done（cancelled）。
   * 取消接口失败，或说已经没有这次运行（done 正在路上）时，回到进行中，可以再点一次。
   * 本页没有这次运行时只调用取消接口。
   */
  function stopRun(conversationId: string): void {
    const run = snapshot.runs.get(conversationId);
    if (run === undefined) {
      void cancelConversation(conversationId).catch(() => undefined);
      return;
    }
    if (run.phase !== "running") return;
    setRun(conversationId, (current) => ({ ...current, phase: "stopping" }));
    const resume = () => setRun(conversationId, (current) => (current.phase === "stopping" ? { ...current, phase: "running" } : current));
    cancelConversation(conversationId).then((response) => {
      if (!response.data.cancelled) resume();
    }, resume);
  }

  /** 登出或换用户：中止所有运行（含正在接回的连接）并清空。 */
  function reset(): void {
    epoch += 1;
    for (const controller of controllers.values()) controller.abort();
    controllers.clear();
    publish({ runs: new Map(), ends: new Map(), detached: new Map() });
  }

  return {
    subscribe: (listener) => {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    getSnapshot: () => snapshot,
    startRun,
    attachRun,
    stopRun,
    reset,
  };
}

const store = createRunStore();

export const subscribeRuns = store.subscribe;
export const getRunSnapshot = store.getSnapshot;
export const startRun = store.startRun;
export const attachRun = store.attachRun;
export const stopRun = store.stopRun;
/** 登出或换用户：中止所有运行并清空。 */
export const resetRuns = store.reset;
