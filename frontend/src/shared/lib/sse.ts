// SSE 的按行解析：播放区事件流与管家的运行流共用（两处都用 fetch + ReadableStream 读，要拿到状态码，401 经会话边界处理）。

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
