import { describe, expect, it } from "vitest";
import { failureOf, isHttpFailure, isOutcomeUncertain, ApiError, type ApiFailure } from "./errors";

const http = (status: number, code = "x"): ApiFailure => ({ kind: "http", status, code, message: "m", requestId: "web-1" });

describe("isOutcomeUncertain", () => {
  it("没有拿到可信响应时结果不确定", () => {
    expect(isOutcomeUncertain({ kind: "network", requestId: "web-1" })).toBe(true);
    expect(isOutcomeUncertain({ kind: "invalid_response", status: 200, requestId: "web-1" })).toBe(true);
    expect(isOutcomeUncertain({ kind: "invalid_response", status: 404, requestId: "web-1" })).toBe(true);
  });

  it("5xx 结果不确定，4xx 是明确的拒绝", () => {
    expect(isOutcomeUncertain(http(500, "operation_failed"))).toBe(true);
    expect(isOutcomeUncertain(http(503, "database_unavailable"))).toBe(true);
    for (const status of [400, 401, 403, 404, 409]) expect(isOutcomeUncertain(http(status))).toBe(false);
  });

  it("主动取消与不是 ApiError 的错误不算结果不确定", () => {
    expect(isOutcomeUncertain({ kind: "aborted" })).toBe(false);
    expect(isOutcomeUncertain(null)).toBe(false);
  });
});

describe("failureOf 与 isHttpFailure", () => {
  it("取出 ApiError 的分类，其他错误为 null", () => {
    const failure = http(409, "revision_conflict");
    expect(failureOf(new ApiError(failure))).toBe(failure);
    expect(failureOf(new Error("boom"))).toBeNull();
    expect(failureOf("boom")).toBeNull();
  });

  it("按状态与错误码判断 http 失败", () => {
    const failure = http(409, "revision_conflict");
    expect(isHttpFailure(failure, 409)).toBe(true);
    expect(isHttpFailure(failure, 409, "revision_conflict")).toBe(true);
    expect(isHttpFailure(failure, 409, "idempotency_conflict")).toBe(false);
    expect(isHttpFailure(failure, 404)).toBe(false);
    expect(isHttpFailure({ kind: "network", requestId: "web-1" }, 409)).toBe(false);
    expect(isHttpFailure(null, 409)).toBe(false);
  });
});
