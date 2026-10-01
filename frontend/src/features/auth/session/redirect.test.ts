import { describe, expect, it } from "vitest";
import { sanitizeRedirect } from "./redirect";

describe("sanitizeRedirect", () => {
  it.each(["/", "/library", "/library/scans/123e4567-e89b-42d3-a456-426614174000", "/members?tab=1#top", "/a%20b"])(
    "接受站内路径 %s",
    (value) => {
      expect(sanitizeRedirect(value)).toBe(value);
    },
  );

  it.each([
    ["协议相对地址", "//evil.example/path"],
    ["绝对地址", "https://evil.example/"],
    ["javascript 协议", "javascript:alert(1)"],
    ["不以斜杠开头", "library"],
    ["反斜杠绕过", "/\\evil.example"],
    ["路径中的反斜杠", "/a\\b"],
    ["换行", "/library\nSet-Cookie: x=1"],
    ["制表符", "/a\tb"],
    ["空字符串", ""],
    ["登录页", "/login"],
    ["登录页带查询", "/login?redirect=/library"],
    ["登录页子路径", "/login/x"],
    ["初始化页", "/setup"],
    ["初始化页带锚点", "/setup#x"],
  ])("拒绝：%s", (_name, value) => {
    expect(sanitizeRedirect(value)).toBeUndefined();
  });

  it("不是字符串的值被丢弃", () => {
    for (const value of [undefined, null, 1, {}, ["/library"]]) {
      expect(sanitizeRedirect(value)).toBeUndefined();
    }
  });

  it("长度上限 2048 个字符", () => {
    expect(sanitizeRedirect("/" + "a".repeat(2047))).toBeDefined();
    expect(sanitizeRedirect("/" + "a".repeat(2048))).toBeUndefined();
  });

  it("名字以 /login 开头但不是登录页的路径可以通过", () => {
    expect(sanitizeRedirect("/loginhelp")).toBe("/loginhelp");
  });
});
