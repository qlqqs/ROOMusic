// shared/api 的公开入口：请求、错误分类、解码原语、重试策略与失败文案。
// 解码原语以命名空间导出，feature 写作 `import { decode as d }` 后使用 `d.object({ id: d.uuid() })`。
export * as decode from "./decode";
export { decodeErrorEnvelope, type ErrorEnvelope } from "./envelope";
export { ApiError, failureOf, isApiError, isHttpFailure, isOutcomeUncertain, type ApiFailure } from "./errors";
export { describeFailure, type FailureContext, type FailureDescription, type FailureTextOptions } from "./failure-text";
export {
  apiRequest,
  setSessionBoundary,
  type ApiPath,
  type ApiRequestSpec,
  type ApiResponse,
  type SessionBoundary,
} from "./request";
export { MAX_QUERY_RETRIES, shouldRetryQuery } from "./retry";
