import { describe, expect, it } from "vitest";
import { ApiError, type ApiFailure } from "./errors";
import { shouldRetryQuery } from "./retry";

const http = (status: number): ApiError =>
  new ApiError({ kind: "http", status, code: "x", message: "m", requestId: "r" });

describe("shouldRetryQuery", () => {
  it("网络失败与 503 最多重试 2 次", () => {
    const network = new ApiError({ kind: "network", requestId: "r" });
    for (const error of [network, http(503)]) {
      expect(shouldRetryQuery(0, error)).toBe(true);
      expect(shouldRetryQuery(1, error)).toBe(true);
      expect(shouldRetryQuery(2, error)).toBe(false);
    }
  });

  it.each<[string, ApiFailure]>([
    ["4xx", { kind: "http", status: 404, code: "not_found", message: "m", requestId: "r" }],
    ["401", { kind: "http", status: 401, code: "unauthorized", message: "m", requestId: "r" }],
    ["500", { kind: "http", status: 500, code: "internal", message: "m", requestId: "r" }],
    ["无效响应", { kind: "invalid_response", status: 200, requestId: "r" }],
    ["取消", { kind: "aborted" }],
  ])("%s 不重试", (_name, failure) => {
    expect(shouldRetryQuery(0, new ApiError(failure))).toBe(false);
  });

  it("不是 ApiError 的错误不重试", () => {
    expect(shouldRetryQuery(0, new Error("boom"))).toBe(false);
    expect(shouldRetryQuery(0, "boom")).toBe(false);
  });
});
