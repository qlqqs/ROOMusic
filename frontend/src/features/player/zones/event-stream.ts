// 事件流（合同 §5）：fetch + ReadableStream 按行解析 SSE（不用 EventSource：要拿到状态码，401 经会话边界处理）。
// 断开按 1、2、5、10 秒退避重连，之后每 10 秒；页面重新可见时立即重连。控制端标签页隐藏 30 秒后断开（HTTP/1.1 每个主机只有 6 条连接），
// 输出端标签页始终保持（流连着即视为输出端在线）。重连后服务端先发快照，状态靠它对齐，不补发。
import { failureOf } from "../../../shared/api";
import { decodeStreamEvent, type StreamEvent } from "./decoders";

const RECONNECT_DELAYS_MS = [1_000, 2_000, 5_000, 10_000] as const;
const HIDDEN_DISCONNECT_MS = 30_000;

/** SSE 的一条消息：事件类型与 data（多行 data 以换行连接）。 */
export type SseMessage = { event: string; data: string };

/** 按行解析 SSE：注释行（以“:”开头，如心跳）忽略，空行结束一条消息；没有 event 字段的消息类型是 message。 */
export class SseParser {
  private buffer = "";
  private event = "";
  private data: string[] = [];

  push(chunk: string): SseMessage[] {
    this.buffer += chunk;
    const messages: SseMessage[] = [];
    for (;;) {
      const newline = this.buffer.search(/\r\n|\r|\n/);
      if (newline < 0) break;
      const line = this.buffer.slice(0, newline);
      const width = this.buffer.startsWith("\r\n", newline) ? 2 : 1;
      this.buffer = this.buffer.slice(newline + width);
      if (line === "") {
        if (this.data.length > 0) messages.push({ event: this.event === "" ? "message" : this.event, data: this.data.join("\n") });
        this.event = "";
        this.data = [];
        continue;
      }
      if (line.startsWith(":")) continue;
      const colon = line.indexOf(":");
      const field = colon < 0 ? line : line.slice(0, colon);
      let value = colon < 0 ? "" : line.slice(colon + 1);
      if (value.startsWith(" ")) value = value.slice(1);
      if (field === "event") this.event = value;
      else if (field === "data") this.data.push(value);
    }
    return messages;
  }
}

export type EventStreamDeps = {
  open: (outputSessionId: string | null, signal: AbortSignal) => Promise<Response>;
  /** outputSessionId：这条连接打开时带的输出会话（控制端为 null）。 */
  onEvent: (event: StreamEvent, outputSessionId: string | null) => void;
  /** 页面可见性；没有 document 的环境（测试）可以不给。 */
  visibility?: { hidden: () => boolean; subscribe: (listener: () => void) => () => void } | undefined;
};

/** 浏览器的页面可见性。 */
export function documentVisibility(): NonNullable<EventStreamDeps["visibility"]> {
  return {
    hidden: () => document.visibilityState === "hidden",
    subscribe: (listener) => {
      document.addEventListener("visibilitychange", listener);
      return () => document.removeEventListener("visibilitychange", listener);
    },
  };
}

export class EventStream {
  private readonly deps: EventStreamDeps;
  private outputSessionId: string | null = null;
  /** 每次启动或换输出会话加一：旧循环据此退出。 */
  private token = 0;
  private running = false;
  /** 控制端标签页隐藏太久而断开。 */
  private suspended = false;
  /** 401 之后不再重连：会话边界会结束整个已登录子树。 */
  private unauthorized = false;
  private abort: AbortController | null = null;
  private wake: (() => void) | null = null;
  private hiddenTimer: ReturnType<typeof setTimeout> | null = null;
  private unsubscribeVisibility: (() => void) | null = null;

  constructor(deps: EventStreamDeps) {
    this.deps = deps;
  }

  start(): void {
    if (this.running) return;
    this.running = true;
    this.unsubscribeVisibility = this.deps.visibility?.subscribe(this.onVisibilityChange) ?? null;
    this.connect();
  }

  stop(): void {
    this.running = false;
    this.token += 1;
    this.abort?.abort();
    this.abort = null;
    this.wake = null;
    this.clearHiddenTimer();
    this.unsubscribeVisibility?.();
    this.unsubscribeVisibility = null;
  }

  /** 本标签页成为（或不再是）输出端：换一条带输出会话的流。 */
  setOutputSession(sessionId: string | null): void {
    if (sessionId === this.outputSessionId) return;
    this.outputSessionId = sessionId;
    if (sessionId !== null) {
      this.clearHiddenTimer();
      this.suspended = false;
    }
    if (this.running) this.connect();
  }

  private connect(): void {
    this.token += 1;
    this.abort?.abort();
    this.abort = null;
    this.wake = null;
    if (this.unauthorized || this.suspended) return;
    void this.run(this.token);
  }

  private async run(token: number): Promise<void> {
    let attempt = 0;
    while (this.token === token) {
      const controller = new AbortController();
      this.abort = controller;
      const session = this.outputSessionId;
      try {
        const response = await this.deps.open(session, controller.signal);
        if (response.body === null) throw new Error("事件流没有响应体");
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        const parser = new SseParser();
        for (;;) {
          const { done, value } = await reader.read();
          if (done || this.token !== token) break;
          for (const message of parser.push(decoder.decode(value, { stream: true }))) {
            let event: StreamEvent | null;
            try {
              event = decodeStreamEvent(message.event, message.data);
            } catch {
              // 解不了的事件丢掉：队列序号对不上时会整段重读，下一次重连的快照也会对齐状态。
              continue;
            }
            if (event === null) continue;
            if (event.type === "snapshot") attempt = 0;
            this.deps.onEvent(event, session);
          }
        }
        controller.abort();
      } catch (error) {
        if (this.token !== token) return;
        const failure = failureOf(error);
        if (failure?.kind === "http" && failure.status === 401) {
          this.unauthorized = true;
          return;
        }
      }
      if (this.token !== token) return;
      await this.delay(RECONNECT_DELAYS_MS[Math.min(attempt, RECONNECT_DELAYS_MS.length - 1)] ?? 10_000);
      attempt += 1;
    }
  }

  private delay(ms: number): Promise<void> {
    return new Promise((resolve) => {
      const timer = setTimeout(() => {
        this.wake = null;
        resolve();
      }, ms);
      this.wake = () => {
        clearTimeout(timer);
        this.wake = null;
        resolve();
      };
    });
  }

  private readonly onVisibilityChange = (): void => {
    if (!this.running) return;
    const hidden = this.deps.visibility?.hidden() ?? false;
    if (hidden) {
      if (this.outputSessionId !== null || this.hiddenTimer !== null) return;
      this.hiddenTimer = setTimeout(() => {
        this.hiddenTimer = null;
        this.suspended = true;
        this.token += 1;
        this.abort?.abort();
        this.abort = null;
        this.wake = null;
      }, HIDDEN_DISCONNECT_MS);
      return;
    }
    this.clearHiddenTimer();
    if (this.suspended) {
      this.suspended = false;
      this.connect();
      return;
    }
    // 正在退避等待：立即重连。
    this.wake?.();
  };

  private clearHiddenTimer(): void {
    if (this.hiddenTimer !== null) clearTimeout(this.hiddenTimer);
    this.hiddenTimer = null;
  }
}
