import { describe, expect, it } from "vitest";
import { isUuid, newIdempotencyKey, newRequestId } from "./ids";

describe("newRequestId", () => {
  it("生成 web- 加 16 位十六进制，满足后端 X-Request-ID 的格式", () => {
    const id = newRequestId();
    expect(id).toMatch(/^web-[0-9a-f]{16}$/);
    expect(id).toMatch(/^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$/);
  });

  it("多次生成互不相同", () => {
    const ids = new Set(Array.from({ length: 50 }, () => newRequestId()));
    expect(ids.size).toBe(50);
  });
});

describe("newIdempotencyKey", () => {
  it("生成 UUID v4", () => {
    expect(newIdempotencyKey()).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
  });

  it("多次生成互不相同", () => {
    const keys = new Set(Array.from({ length: 50 }, () => newIdempotencyKey()));
    expect(keys.size).toBe(50);
  });
});

describe("isUuid", () => {
  it("接受带连字符的 UUID，不区分大小写", () => {
    expect(isUuid("00000000-0000-4000-8000-000000000001")).toBe(true);
    expect(isUuid("ABCDEF00-0000-4000-8000-00000000000A")).toBe(true);
    expect(isUuid(newIdempotencyKey())).toBe(true);
  });

  it("拒绝其他形状与非字符串", () => {
    for (const value of ["", "abc", "00000000000040008000000000000001", "00000000-0000-4000-8000-00000000000g", " 00000000-0000-4000-8000-000000000001", "../00000000-0000-4000-8000-000000000001", 12, null, undefined]) {
      expect(isUuid(value)).toBe(false);
    }
  });
});
