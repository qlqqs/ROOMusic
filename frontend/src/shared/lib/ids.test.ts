import { describe, expect, it } from "vitest";
import { newIdempotencyKey, newRequestId } from "./ids";

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
