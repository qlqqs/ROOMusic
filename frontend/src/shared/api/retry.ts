import { failureOf } from "./errors";

/** 查询最多自动重试的次数；写操作从不自动重试（TanStack Query 的 mutation 默认不重试）。 */
export const MAX_QUERY_RETRIES = 2;

/**
 * 查询只对网络失败与 503 重试：4xx 与无效响应重试不会得到不同的结果，
 * 取消不是失败。failureCount 是已经失败的次数，首次失败时为 0。
 */
export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  if (failureCount >= MAX_QUERY_RETRIES) return false;
  const failure = failureOf(error);
  if (failure === null) return false;
  return failure.kind === "network" || (failure.kind === "http" && failure.status === 503);
}
