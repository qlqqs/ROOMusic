import { vi } from "vitest";
import { loadApiSample } from "./api-samples";

/** 测试用的 JSON 响应；默认带 application/json，与后端的 WriteJSON 一致。 */
export function jsonResponse(status: number, body: unknown, headers: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

/** 用后端真实响应样本构造响应（状态码与响应体都取自样本）。 */
export function sampleResponse(name: string, headers: Record<string, string> = {}): Response {
  const sample = loadApiSample(name);
  return jsonResponse(sample.status, sample.body, headers);
}

/** 一次请求的记录：测试据此断言路径、方法、请求头与请求体。 */
export type RecordedRequest = {
  path: string;
  method: string;
  headers: Headers;
  body: unknown;
  signal: AbortSignal | null;
  init: RequestInit;
};

export type FetchHandler = (request: RecordedRequest) => Response | Promise<Response>;

/**
 * 用桩替换全局 fetch，并返回已记录的请求列表。桩在每个测试后由 vitest 的 unstubGlobals 还原。
 * handler 抛出的错误会作为 fetch 的拒绝抛出，用来模拟网络失败。
 */
export function stubFetch(handler: FetchHandler): RecordedRequest[] {
  const requests: RecordedRequest[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}): Promise<Response> => {
      const recorded: RecordedRequest = {
        path: typeof input === "string" ? input : input instanceof URL ? input.pathname + input.search : input.url,
        method: init.method ?? "GET",
        headers: new Headers(init.headers),
        body: typeof init.body === "string" ? (JSON.parse(init.body) as unknown) : undefined,
        signal: init.signal ?? null,
        init,
      };
      requests.push(recorded);
      return handler(recorded);
    }),
  );
  return requests;
}
