import { describe, expect, it } from "vitest";
import { MAX_PASSWORD_LENGTH, MIN_PASSWORD_LENGTH, utf8ByteLength, validateNewAccount, validateNewPassword, validatePasswordChange } from "./account-rules";

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


describe("validateNewPassword：改密码与重置共用的完整边界", () => {
  it.each([
    ["空密码", "", "密码至少要 12 位。"],
    ["11 位", "a".repeat(11), "密码至少要 12 位。"],
    ["12 位", "a".repeat(12), undefined],
    ["72 位", "a".repeat(72), undefined],
    ["73 位", "a".repeat(73), "密码太长了，请短一些。"],
    ["中文", "password1234密", "密码只能用英文字母、数字和符号。"],
    ["全角", "ｐａｓｓｗｏｒｄ１２３４", "密码只能用英文字母、数字和符号。"],
    ["emoji", "password1234😀", "密码只能用英文字母、数字和符号。"],
    ["NUL", "password1234\0", "密码只能用英文字母、数字和符号。"],
    ["制表符", "password1234\t", "密码只能用英文字母、数字和符号。"],
    ["换行", "password1234\n", "密码只能用英文字母、数字和符号。"],
    ["回车", "password1234\r", "密码只能用英文字母、数字和符号。"],
    ["ASCII 下界之前", "password1234\x1f", "密码只能用英文字母、数字和符号。"],
    ["DEL", "password1234\x7f", "密码只能用英文字母、数字和符号。"],
    ["ASCII 之后", "password1234\x80", "密码只能用英文字母、数字和符号。"],
    ["不换行空格", "password1234\u00a0", "密码只能用英文字母、数字和符号。"],
    ["ASCII 下界空格", " ".repeat(12), undefined],
    ["ASCII 上界波浪线", "~".repeat(12), undefined],
    ["首尾空格保留", " password12 ", undefined],
  ])("%s", (_name, password, message) => {
    expect(validateNewPassword(password, password)).toEqual(message === undefined ? {} : { password: message });
  });

  it("确认输入严格相等，不去空格；密码错误优先于确认错误", () => {
    expect(validateNewPassword(" password12 ", "password12")).toEqual({ confirmation: "两次输入的密码不一致。" });
    expect(validateNewPassword("abcdefghijkl", "")).toEqual({ confirmation: "两次输入的密码不一致。" });
    expect(validateNewPassword("short", "different")).toEqual({ password: "密码至少要 12 位。" });
    expect(validateNewPassword("密".repeat(73), "different")).toEqual({ password: "密码只能用英文字母、数字和符号。" });
  });
});

describe("validatePasswordChange", () => {
  const next = { password: "abcdefghijkl", confirmation: "abcdefghijkl" };

  it("管理员不需要当前密码", () => {
    expect(validatePasswordChange({ current: null, ...next })).toEqual({});
  });

  it("空当前密码需要提示，并保留新密码与确认错误", () => {
    expect(validatePasswordChange({ current: "", ...next })).toEqual({ current: "请输入当前密码。" });
    expect(validatePasswordChange({ current: "", password: "short", confirmation: "other" })).toEqual({
      current: "请输入当前密码。", password: "密码至少要 12 位。",
    });
    expect(validatePasswordChange({ current: "", ...next, confirmation: "different" })).toEqual({
      current: "请输入当前密码。", confirmation: "两次输入的密码不一致。",
    });
  });

  it.each(["x", "旧密码", " ", "a".repeat(73)])("当前密码 %s 只校验非空，旧账号仍能改密码", (current) => {
    expect(validatePasswordChange({ current, ...next })).toEqual({});
  });

  it.each(["", "a".repeat(11), "a".repeat(12), "a".repeat(72), "a".repeat(73), "password1234密", "password1234\t"])("新密码 %j 复用同一规则", (password) => {
    expect(validatePasswordChange({ current: "old", password, confirmation: password })).toEqual(validateNewPassword(password, password));
  });

  it("允许新旧密码相同，也可以只退出其他设备", () => {
    expect(validatePasswordChange({ current: next.password, ...next })).toEqual({});
  });
});
