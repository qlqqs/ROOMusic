import { describe, expect, it } from "vitest";
import { MAX_PASSWORD_LENGTH, MIN_PASSWORD_LENGTH, utf8ByteLength, validateNewAccount } from "./account-rules";

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
  const withPassword = (password: string) => validateNewAccount({ ...valid, password, confirmation: password });

  it("合法输入没有错误", () => {
    expect(validateNewAccount(valid)).toEqual({});
  });

  it("用户名去首尾空白后不能为空", () => {
    expect(validateNewAccount({ ...valid, username: "   " }).username).toBeDefined();
    expect(validateNewAccount({ ...valid, username: " a " }).username).toBeUndefined();
  });

  it("密码至少 12 位：11 位不够，12 位可以", () => {
    expect(withPassword("a".repeat(MIN_PASSWORD_LENGTH - 1)).password).toBe("密码至少要 12 位。");
    expect(withPassword("a".repeat(MIN_PASSWORD_LENGTH))).toEqual({});
    expect(withPassword("").password).toBe("密码至少要 12 位。");
  });

  it("密码最多 72 位（bcrypt 的上限）：72 位可以，73 位只提示密码太长", () => {
    expect(withPassword("a".repeat(MAX_PASSWORD_LENGTH))).toEqual({});
    expect(withPassword("a".repeat(MAX_PASSWORD_LENGTH + 1)).password).toBe("密码太长了，请短一些。");
  });

  it("空格与各种符号都可以用", () => {
    expect(withPassword("correct horse battery")).toEqual({});
    expect(withPassword(" !\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~")).toEqual({});
  });

  it("中文、全角字符、emoji、制表符都不行，即使长度够也先提示字符", () => {
    const message = "密码只能用英文字母、数字和符号。";
    expect(withPassword("密码密码密码密码密码密码").password).toBe(message);
    expect(withPassword("password1234密").password).toBe(message);
    expect(withPassword("ｐａｓｓｗｏｒｄ１２３４").password).toBe(message);
    expect(withPassword("password1234　").password).toBe(message);
    expect(withPassword("password1234😀").password).toBe(message);
    expect(withPassword("password\t1234").password).toBe(message);
    expect(withPassword("密").password).toBe(message);
  });

  it("两次输入的密码必须一致；密码本身不合格时只提示密码", () => {
    expect(validateNewAccount({ ...valid, confirmation: "different-password-1" }).confirmation).toBeDefined();
    const errors = validateNewAccount({ ...valid, password: "short", confirmation: "other" });
    expect(errors.password).toBeDefined();
    expect(errors.confirmation).toBeUndefined();
  });
});
