// 输出端回报的发送（design-protocol.md §3.3）：同一个输出端串行发送，一次只有一个在途；
// 状态类回报合并成最新一条，失败直接丢弃；事件类回报（ended、fault）失败时保留，随下一次回报重发（重复的在服务端是无操作）。
import type { ReportBody } from "../zones/api";

export class ReportQueue<R> {
  private readonly post: (body: ReportBody) => Promise<R>;
  private readonly onResult: (result: R) => void;
  private readonly onError: (error: unknown) => void;
  private readonly events: ReportBody[] = [];
  private status: ReportBody | null = null;
  private inflight = false;
  private closed = false;

  constructor(post: (body: ReportBody) => Promise<R>, onResult: (result: R) => void, onError: (error: unknown) => void) {
    this.post = post;
    this.onResult = onResult;
    this.onError = onError;
  }

  send(body: ReportBody): void {
    if (this.closed) return;
    if (body.kind === "status") this.status = body;
    else if (!this.events.some((held) => held.kind === body.kind && held.fault === body.fault && held.stream_seq === body.stream_seq)) this.events.push(body);
    this.pump();
  }

  /** 新的输出会话：重新开始发送。 */
  reopen(): void {
    this.closed = false;
  }

  /** 输出会话结束：之后的回报都不再发。 */
  close(): void {
    this.closed = true;
    this.events.length = 0;
    this.status = null;
  }

  private pump(): void {
    if (this.inflight || this.closed) return;
    const event = this.events[0];
    const body = event ?? this.status;
    if (body === null) return;
    if (event === undefined) this.status = null;
    this.inflight = true;
    this.post(body).then(
      (result) => {
        this.inflight = false;
        if (this.closed) return;
        if (event !== undefined) this.events.shift();
        this.onResult(result);
        this.pump();
      },
      (error: unknown) => {
        this.inflight = false;
        if (this.closed) return;
        // 事件类留在队首，等下一次回报时一起重发；不在这里立即重试。
        this.onError(error);
      },
    );
  }
}
