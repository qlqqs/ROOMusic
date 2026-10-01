import { describe, expect, it } from "vitest";
import { DecodeError } from "./decode";
import { decodeErrorEnvelope } from "./envelope";

describe("decodeErrorEnvelope", () => {
  const valid = { error: { code: "revision_conflict", message: "目录已变化" }, request_id: "web-0123456789abcdef" };

  it("解码合法的错误信封", () => {
    expect(decodeErrorEnvelope(valid)).toEqual(valid);
  });

  it("允许空的 request_id（降级 handler 可能不回写）", () => {
    expect(decodeErrorEnvelope({ ...valid, request_id: "" }).request_id).toBe("");
  });

  it.each([
    ["缺少 error", { request_id: "x" }],
    ["缺少 request_id", { error: valid.error }],
    ["code 含大写或连字符", { ...valid, error: { code: "Bad-Code", message: "m" } }],
    ["code 为空", { ...valid, error: { code: "", message: "m" } }],
    ["code 超过 64 字符", { ...valid, error: { code: "a".repeat(65), message: "m" } }],
    ["message 不是字符串", { ...valid, error: { code: "x", message: 1 } }],
    ["message 超过 1000 字符", { ...valid, error: { code: "x", message: "m".repeat(1001) } }],
  ])("拒绝不合法的信封：%s", (_name, input) => {
    expect(() => decodeErrorEnvelope(input)).toThrow(DecodeError);
  });
});
