import { describe, expect, it } from "vitest";
import { MAX_PASSWORD_BYTES, MIN_PASSWORD_BYTES, utf8ByteLength, validateNewAccount } from "./account-rules";

describe("utf8ByteLength", () => {
  it("按 UTF-8 字节计算：英文 1 字节、中文 3 字节、emoji 4 字节", () => {
    expect(utf8ByteLength("abc")).toBe(3);
    expect(utf8ByteLength("密码")).toBe(6);
    expect(utf8ByteLength("😀")).toBe(4);
    expect(utf8ByteLength("")).toBe(0);
  });
});

describe("validateNewAccount", () => {
  const valid = { username: "curator", password: "a-long-enough-password", confirmation: "a-long-enough-password" };

  it("合法输入没有错误", () => {
    expect(validateNewAccount(valid)).toEqual({});
  });

  it("用户名去首尾空白后不能为空", () => {
    expect(validateNewAccount({ ...valid, username: "   " }).username).toBeDefined();
    expect(validateNewAccount({ ...valid, username: " a " }).username).toBeUndefined();
  });

  it("密码至少 12 个字节：4 个中文字符恰好 12 字节，3 个中文字符不够", () => {
    expect(validateNewAccount({ ...valid, password: "密码密码", confirmation: "密码密码" })).toEqual({});
    expect(validateNewAccount({ ...valid, password: "密码密", confirmation: "密码密" }).password).toContain(String(MIN_PASSWORD_BYTES));
    expect(validateNewAccount({ ...valid, password: "12345678901", confirmation: "12345678901" }).password).toBeDefined();
    expect(validateNewAccount({ ...valid, password: "123456789012", confirmation: "123456789012" })).toEqual({});
  });

  it("密码最多 72 个字节（bcrypt 的上限）：72 字节可以，73 字节不行，提示里带当前字节数", () => {
    const atLimit = "a".repeat(MAX_PASSWORD_BYTES);
    expect(validateNewAccount({ ...valid, password: atLimit, confirmation: atLimit })).toEqual({});
    const over = "a".repeat(MAX_PASSWORD_BYTES + 1);
    expect(validateNewAccount({ ...valid, password: over, confirmation: over }).password).toBe("密码不能超过 72 个字节（现在是 73 个）。");
    // 24 个中文字符恰好 72 字节，25 个是 75 字节。
    expect(validateNewAccount({ ...valid, password: "字".repeat(24), confirmation: "字".repeat(24) })).toEqual({});
    expect(validateNewAccount({ ...valid, password: "字".repeat(25), confirmation: "字".repeat(25) }).password).toContain("75");
  });

  it("两次输入的密码必须一致；密码本身不合格时只提示密码", () => {
    expect(validateNewAccount({ ...valid, confirmation: "different-password-1" }).confirmation).toBeDefined();
    const errors = validateNewAccount({ ...valid, password: "short", confirmation: "other" });
    expect(errors.password).toBeDefined();
    expect(errors.confirmation).toBeUndefined();
  });
});
