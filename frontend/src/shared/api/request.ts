import { newRequestId } from "../lib/ids";
import { decodeErrorEnvelope } from "./envelope";
import { ApiError } from "./errors";

export type ApiPath = `/api/v1/${string}`;

export type ApiRequestSpec = {
  method: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  path: ApiPath;
  body?: unknown;
  idempotencyKey?: string | undefined;
  signal?: AbortSignal | undefined;
  /** 页面卸载时仍要发出的请求（输出端的 release）。 */
  keepalive?: boolean;
  /** "required"（默认）的请求得到 401 unauthorized 时通知会话边界；初始化状态、初始化与登录接口用 "none"。 */
  session?: "required" | "none";
};

/** 返回状态码，因为 201（新增）、202/200（扫描开始与取消）带有业务含义。 */
export type ApiResponse<T> = { status: number; data: T };

/** 请求层与会话生命周期之间的唯一接线：由应用启动时注册。 */
export type SessionBoundary = {
  /** 请求发出时读取的会话代次；401 到达时随回调带回，让会话控制器忽略属于旧会话的 401。 */
  currentGeneration: () => number;
  onUnauthorized: (generation: number) => void;
};

let sessionBoundary: SessionBoundary | null = null;

export function setSessionBoundary(boundary: SessionBoundary | null): void {
  sessionBoundary = boundary;
}

const jsonContentType = /^application\/(?:[\w.+-]+\+)?json\s*(?:;|$)/i;

type JsonBody = { ok: true; value: unknown } | { ok: false };

async function readJsonBody(response: Response): Promise<JsonBody> {
  if (!jsonContentType.test(response.headers.get("Content-Type") ?? "")) return { ok: false };
  const text = await response.text();
  try {
    const value: unknown = JSON.parse(text);
    return { ok: true, value };
  } catch {
    return { ok: false };
  }
}

/**
 * 唯一发出 /api/v1 请求的函数：同源、携带 Cookie、不使用浏览器缓存、带客户端请求 ID。
 * 只有 JSON 响应进入解码；失败时抛出携带 ApiFailure 的 ApiError。
 * decode 的第二个参数是成功响应的状态码：同一路径的 200 与 202 形状不同时（播放预检），解码器借它核对状态码与响应体相符；
 * 其余只关心响应体的解码器可以忽略它。
 */
export async function apiRequest<T>(spec: ApiRequestSpec, decode: (input: unknown, status: number) => T): Promise<ApiResponse<T>> {
  const requestId = newRequestId();
  const generation = sessionBoundary?.currentGeneration() ?? 0;

  const headers = new Headers({ Accept: "application/json", "X-Request-ID": requestId });
  const init: RequestInit = { method: spec.method, headers, credentials: "same-origin", cache: "no-store" };
  if (spec.signal !== undefined) init.signal = spec.signal;
  if (spec.keepalive === true) init.keepalive = true;
  if (spec.body !== undefined) {
    headers.set("Content-Type", "application/json");
    init.body = JSON.stringify(spec.body);
  }
  if (spec.idempotencyKey !== undefined) headers.set("Idempotency-Key", spec.idempotencyKey);

  let response: Response;
  let body: JsonBody;
  try {
    response = await fetch(spec.path, init);
    // 204 没有响应体：解码器拿到 null。
    if (response.status === 204) return { status: 204, data: decode(null, 204) };
    body = await readJsonBody(response);
  } catch (error) {
    if (spec.signal?.aborted) throw new ApiError({ kind: "aborted" }, { cause: error });
    throw new ApiError({ kind: "network", requestId }, { cause: error });
  }

  const { status } = response;
  if (!body.ok) throw new ApiError({ kind: "invalid_response", status, requestId });

  if (!response.ok) {
    let envelope: ReturnType<typeof decodeErrorEnvelope>;
    try {
      envelope = decodeErrorEnvelope(body.value);
    } catch (error) {
      throw new ApiError({ kind: "invalid_response", status, requestId }, { cause: error });
    }
    if (status === 401 && envelope.error.code === "unauthorized" && spec.session !== "none") {
      sessionBoundary?.onUnauthorized(generation);
    }
    throw new ApiError({
      kind: "http",
      status,
      code: envelope.error.code,
      message: envelope.error.message,
      requestId: envelope.request_id === "" ? requestId : envelope.request_id,
    });
  }

  try {
    return { status, data: decode(body.value, status) };
  } catch (error) {
    throw new ApiError({ kind: "invalid_response", status, requestId }, { cause: error });
  }
}

/**
 * 打开一条长响应（播放区事件流 text/event-stream）：与 apiRequest 同样同源、带 Cookie、不缓存、带请求 ID。
 * 200 时把响应交给调用方逐块读取；其他状态按错误信封抛出 ApiError，401 unauthorized 同样通知会话边界（带请求发出时的代次）。
 */
export async function apiStream(spec: { path: ApiPath; signal: AbortSignal }): Promise<Response> {
  const requestId = newRequestId();
  const generation = sessionBoundary?.currentGeneration() ?? 0;
  const headers = new Headers({ Accept: "text/event-stream", "X-Request-ID": requestId });
  let response: Response;
  try {
    response = await fetch(spec.path, { method: "GET", headers, credentials: "same-origin", cache: "no-store", signal: spec.signal });
  } catch (error) {
    if (spec.signal.aborted) throw new ApiError({ kind: "aborted" }, { cause: error });
    throw new ApiError({ kind: "network", requestId }, { cause: error });
  }
  if (response.ok && response.body !== null) return response;
  const { status } = response;
  let body: JsonBody;
  try {
    body = await readJsonBody(response);
  } catch (error) {
    throw new ApiError({ kind: "network", requestId }, { cause: error });
  }
  if (response.ok || !body.ok) throw new ApiError({ kind: "invalid_response", status, requestId });
  let envelope: ReturnType<typeof decodeErrorEnvelope>;
  try {
    envelope = decodeErrorEnvelope(body.value);
  } catch (error) {
    throw new ApiError({ kind: "invalid_response", status, requestId }, { cause: error });
  }
  if (status === 401 && envelope.error.code === "unauthorized") sessionBoundary?.onUnauthorized(generation);
  throw new ApiError({ kind: "http", status, code: envelope.error.code, message: envelope.error.message, requestId: envelope.request_id === "" ? requestId : envelope.request_id });
}
