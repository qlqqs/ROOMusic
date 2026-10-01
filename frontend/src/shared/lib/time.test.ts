import { describe, expect, it } from "vitest";
import { formatDateTime, UNKNOWN_TIME } from "./time";

describe("formatDateTime", () => {
  it("按指定时区格式化为 YYYY-MM-DD HH:mm", () => {
    expect(formatDateTime("2026-01-01T00:00:00Z", "UTC")).toBe("2026-01-01 00:00");
    expect(formatDateTime("2026-01-01T00:00:00Z", "Asia/Shanghai")).toBe("2026-01-01 08:00");
  });

  it("午夜显示 00 而不是 24，保留带偏移与小数秒的输入", () => {
    expect(formatDateTime("2026-03-09T00:05:09.123456789+08:00", "Asia/Shanghai")).toBe("2026-03-09 00:05");
  });

  it("无法解析时显示未知占位", () => {
    expect(formatDateTime("不是时间")).toBe(UNKNOWN_TIME);
    expect(formatDateTime("")).toBe(UNKNOWN_TIME);
  });
});
