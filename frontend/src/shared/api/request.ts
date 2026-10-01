import { newRequestId } from "../lib/ids";
import { decodeErrorEnvelope } from "./envelope";
import { ApiError } from "./errors";

export type ApiPath = `/api/v1/${string}`;

export type ApiRequestSpec = {
  method: "GET" | "POST" | "PATCH";
  path: ApiPath;
  body?: unknown;
  idempotencyKey?: string | undefined;
  signal?: AbortSignal | undefined;
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
 */
export async function apiRequest<T>(spec: ApiRequestSpec, decode: (input: unknown) => T): Promise<ApiResponse<T>> {
  const requestId = newRequestId();
  const generation = sessionBoundary?.currentGeneration() ?? 0;

  const headers = new Headers({ Accept: "application/json", "X-Request-ID": requestId });
  const init: RequestInit = { method: spec.method, headers, credentials: "same-origin", cache: "no-store" };
  if (spec.signal !== undefined) init.signal = spec.signal;
  if (spec.body !== undefined) {
    headers.set("Content-Type", "application/json");
    init.body = JSON.stringify(spec.body);
  }
  if (spec.idempotencyKey !== undefined) headers.set("Idempotency-Key", spec.idempotencyKey);

  let response: Response;
  let body: JsonBody;
  try {
    response = await fetch(spec.path, init);
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
    return { status, data: decode(body.value) };
  } catch (error) {
    throw new ApiError({ kind: "invalid_response", status, requestId }, { cause: error });
  }
}
