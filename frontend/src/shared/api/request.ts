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
 * 发出 /api/v1 JSON 请求的函数（上传走 apiUpload）：同源、携带 Cookie、不使用浏览器缓存、带客户端请求 ID。
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
    // 204 没有响应体，202 受理时也可能没有（例如重新巡检）：解码器拿到 null。
    if (response.status === 204 || (response.status === 202 && response.headers.get("Content-Length") === "0")) return { status: response.status, data: decode(null, response.status) };
    body = await readJsonBody(response);
  } catch (error) {
    if (spec.signal?.aborted) throw new ApiError({ kind: "aborted" }, { cause: error });
    throw new ApiError({ kind: "network", requestId }, { cause: error });
  }

  return settle(response.status, body, requestId, generation, spec.session, decode);
}

/** 拿到响应体之后的共同收尾：错误信封转成 ApiError（401 通知会话边界），成功时解码。 */
function settle<T>(status: number, body: JsonBody, requestId: string, generation: number, session: "required" | "none" | undefined, decode: (input: unknown, status: number) => T): ApiResponse<T> {
  if (!body.ok) throw new ApiError({ kind: "invalid_response", status, requestId });

  if (status < 200 || status > 299) {
    let envelope: ReturnType<typeof decodeErrorEnvelope>;
    try {
      envelope = decodeErrorEnvelope(body.value);
    } catch (error) {
      throw new ApiError({ kind: "invalid_response", status, requestId }, { cause: error });
    }
    if (status === 401 && envelope.error.code === "unauthorized" && session !== "none") {
      sessionBoundary?.onUnauthorized(generation);
    }
    throw new ApiError(
      {
        kind: "http",
        status,
        code: envelope.error.code,
        message: envelope.error.message,
        requestId: envelope.request_id === "" ? requestId : envelope.request_id,
      },
      { body: body.value },
    );
  }

  try {
    return { status, data: decode(body.value, status) };
  } catch (error) {
    throw new ApiError({ kind: "invalid_response", status, requestId }, { cause: error });
  }
}

export type ApiUploadSpec = {
  path: ApiPath;
  form: FormData;
  /** 默认 POST；替换一份资源（艺人图片）用 PUT。 */
  method?: "POST" | "PUT";
  /** 写操作的幂等键（结果不确定后的重试沿用同一个）。 */
  idempotencyKey?: string | undefined;
  signal?: AbortSignal | undefined;
  /** 已发出的比例 0–1；浏览器算不出总长时不回调。 */
  onProgress?: (fraction: number) => void;
};

/**
 * 上传文件（multipart）：与 apiRequest 同源、带 Cookie、带请求 ID，响应同样按错误信封与 decoder 处理。
 * fetch 拿不到上传进度，这里用 XMLHttpRequest；Content-Type 由浏览器连同 boundary 一起生成。
 */
export function apiUpload<T>(spec: ApiUploadSpec, decode: (input: unknown, status: number) => T): Promise<ApiResponse<T>> {
  const requestId = newRequestId();
  const generation = sessionBoundary?.currentGeneration() ?? 0;
  return new Promise((resolve, reject) => {
    if (spec.signal?.aborted) {
      reject(new ApiError({ kind: "aborted" }));
      return;
    }
    const xhr = new XMLHttpRequest();
    const abort = () => xhr.abort();
    spec.signal?.addEventListener("abort", abort, { once: true });
    const finish = () => spec.signal?.removeEventListener("abort", abort);
    xhr.open(spec.method ?? "POST", spec.path);
    xhr.setRequestHeader("Accept", "application/json");
    xhr.setRequestHeader("X-Request-ID", requestId);
    if (spec.idempotencyKey !== undefined) xhr.setRequestHeader("Idempotency-Key", spec.idempotencyKey);
    const { onProgress } = spec;
    if (onProgress !== undefined) {
      xhr.upload.addEventListener("progress", (event) => {
        if (event.lengthComputable && event.total > 0) onProgress(event.loaded / event.total);
      });
    }
    xhr.addEventListener("load", () => {
      finish();
      let body: JsonBody = { ok: false };
      if (jsonContentType.test(xhr.getResponseHeader("Content-Type") ?? "")) {
        try {
          body = { ok: true, value: JSON.parse(xhr.responseText) as unknown };
        } catch {
          body = { ok: false };
        }
      }
      try {
        resolve(settle(xhr.status, body, requestId, generation, "required", decode));
      } catch (error) {
        reject(error instanceof Error ? error : new ApiError({ kind: "invalid_response", status: xhr.status, requestId }));
      }
    });
    xhr.addEventListener("error", () => {
      finish();
      reject(new ApiError({ kind: "network", requestId }));
    });
    xhr.addEventListener("abort", () => {
      finish();
      reject(new ApiError({ kind: "aborted" }));
    });
    xhr.send(spec.form);
  });
}

/**
 * 打开一条长响应（播放区事件流、管家的运行流 text/event-stream）：与 apiRequest 同样同源、带 Cookie、不缓存、带请求 ID。
 * 默认 GET；管家发消息与继续运行用 POST（body 省略时不带请求体）。lastEventId 是 SSE 的 Last-Event-ID 头（管家接回运行时带上收到的最后一个事件序号）。
 * 200 时把响应交给调用方逐块读取；204（管家接回时没有这次运行的事件）同样原样返回，由调用方按状态码处理；
 * 其他状态按错误信封抛出 ApiError，401 unauthorized 同样通知会话边界（带请求发出时的代次）。
 */
export async function apiStream(spec: { path: ApiPath; signal: AbortSignal; method?: "GET" | "POST"; body?: unknown; lastEventId?: string }): Promise<Response> {
  const requestId = newRequestId();
  const generation = sessionBoundary?.currentGeneration() ?? 0;
  const headers = new Headers({ Accept: "text/event-stream", "X-Request-ID": requestId });
  if (spec.lastEventId !== undefined) headers.set("Last-Event-ID", spec.lastEventId);
  const init: RequestInit = { method: spec.method ?? "GET", headers, credentials: "same-origin", cache: "no-store", signal: spec.signal };
  if (spec.body !== undefined) {
    headers.set("Content-Type", "application/json");
    init.body = JSON.stringify(spec.body);
  }
  let response: Response;
  try {
    response = await fetch(spec.path, init);
  } catch (error) {
    if (spec.signal.aborted) throw new ApiError({ kind: "aborted" }, { cause: error });
    throw new ApiError({ kind: "network", requestId }, { cause: error });
  }
  if (response.status === 204 || (response.ok && response.body !== null)) return response;
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
