import { afterEach, expect, it, vi } from "vitest";
import { setSessionBoundary } from "../../../shared/api/request";
import { jsonResponse, stubFetch } from "../../../test/http";
import { openEventStream } from "./api";
import { EventStream, SseParser } from "./event-stream";

afterEach(() => {
  setSessionBoundary(null);
  vi.useRealTimers();
});

it("按分块解析事件流，忽略心跳并保留跨块、多行 data", () => {
  const parser = new SseParser();
  expect(parser.push(': ping\n\nevent: zone\nda')).toEqual([]);
  expect(parser.push('ta: {"zone":\ndata: {"revision":')).toEqual([]);
  expect(parser.push('2}}\n\nevent: device\ndata: {"device":{}}\n\n')).toEqual([
    { event: "zone", data: '{"zone":\n{"revision":2}}' },
    { event: "device", data: '{"device":{}}' },
  ]);
});

it("事件流 401 经会话边界携带请求代次通知，之后不再重连", async () => {
  vi.useFakeTimers();
  const onUnauthorized = vi.fn();
  const onEvent = vi.fn();
  setSessionBoundary({ currentGeneration: () => 42, onUnauthorized });
  const requests = stubFetch(() => jsonResponse(401, { error: { code: "unauthorized", message: "会话已失效" }, request_id: "zones-test" }));
  const events = new EventStream({ open: openEventStream, onEvent });
  try {
    events.start();
    await vi.waitFor(() => expect(onUnauthorized).toHaveBeenCalledWith(42));
    await vi.advanceTimersByTimeAsync(60_000);
    expect(onUnauthorized).toHaveBeenCalledTimes(1);
    expect(requests.map((request) => request.path)).toEqual(["/api/v1/me/playback/events"]);
    expect(onEvent).not.toHaveBeenCalled();
  } finally {
    events.stop();
  }
});
