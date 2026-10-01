/**
 * 请求失败的四种分类：
 * - http：后端返回了合法的错误信封；
 * - invalid_response：收到响应但不能信任（非 JSON、HTML 回落页、信封或 decoder 不合法），结果不进入缓存；
 * - network：没有拿到可用的响应；对写请求来说结果未知，不能当作失败或成功；
 * - aborted：调用方取消。
 */
export type ApiFailure =
  | { kind: "http"; status: number; code: string; message: string; requestId: string }
  | { kind: "invalid_response"; status: number; requestId: string }
  | { kind: "network"; requestId: string }
  | { kind: "aborted" };

export class ApiError extends Error {
  readonly failure: ApiFailure;

  constructor(failure: ApiFailure, options?: ErrorOptions) {
    super(describeForDebug(failure), options);
    this.name = "ApiError";
    this.failure = failure;
  }
}

function describeForDebug(failure: ApiFailure): string {
  switch (failure.kind) {
    case "http":
      return `HTTP ${failure.status} ${failure.code}`;
    case "invalid_response":
      return `invalid response (HTTP ${failure.status})`;
    case "network":
      return "network failure";
    case "aborted":
      return "request aborted";
  }
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}

/** 取出 ApiError 携带的分类；不是 ApiError（例如程序错误）时返回 null。 */
export function failureOf(error: unknown): ApiFailure | null {
  return isApiError(error) ? error.failure : null;
}

/** 失败是否是后端返回的指定 HTTP 状态与错误码；code 省略时只比较状态。 */
export function isHttpFailure(failure: ApiFailure | null, status: number, code?: string): boolean {
  return failure?.kind === "http" && failure.status === status && (code === undefined || failure.code === code);
}
