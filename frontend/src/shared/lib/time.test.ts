import { describe, expect, it } from "vitest";
import { formatDateTime, formatRelativeTime, UNKNOWN_TIME } from "./time";

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

describe("formatRelativeTime", () => {
  // 固定的“现在”：上海时间 2026-11-14 10:00。
  const now = Date.parse("2026-11-14T10:00:00+08:00");
  const at = (value: string) => formatRelativeTime(value, now, "Asia/Shanghai");

  it("不到 1 分钟写“刚刚”，比现在晚（时钟偏差）也写“刚刚”", () => {
    expect(at("2026-11-14T10:00:00+08:00")).toBe("刚刚");
    expect(at("2026-11-14T09:59:01+08:00")).toBe("刚刚");
    expect(at("2026-11-14T10:05:00+08:00")).toBe("刚刚");
  });

  it("不到 1 小时写“N 分钟前”，按整分钟向下取", () => {
    expect(at("2026-11-14T09:59:00+08:00")).toBe("1 分钟前");
    expect(at("2026-11-14T09:56:30+08:00")).toBe("3 分钟前");
    expect(at("2026-11-14T09:00:01+08:00")).toBe("59 分钟前");
  });

  it("同一天写“N 小时前”", () => {
    expect(at("2026-11-14T09:00:00+08:00")).toBe("1 小时前");
    expect(at("2026-11-14T00:00:00+08:00")).toBe("10 小时前");
  });

  it("天数按日历日：前一天写“昨天”，哪怕只过了几个小时", () => {
    expect(at("2026-11-13T23:30:00+08:00")).toBe("昨天");
    expect(at("2026-11-13T00:00:00+08:00")).toBe("昨天");
    expect(formatRelativeTime("2026-11-13T23:30:00+08:00", Date.parse("2026-11-14T00:20:00+08:00"), "Asia/Shanghai")).toBe("50 分钟前");
    expect(formatRelativeTime("2026-11-13T22:00:00+08:00", Date.parse("2026-11-14T00:20:00+08:00"), "Asia/Shanghai")).toBe("昨天");
  });

  it("两到六天写“N 天前”，七天及更早写日期", () => {
    expect(at("2026-11-12T10:00:00+08:00")).toBe("2 天前");
    expect(at("2026-11-08T08:00:00+08:00")).toBe("6 天前");
    expect(at("2026-11-07T10:00:00+08:00")).toBe("2026-11-07");
    expect(at("2025-12-31T10:00:00+08:00")).toBe("2025-12-31");
  });

  it("日历日按给定时区算：同一时刻在 UTC 是同一天、在上海已是第二天", () => {
    const value = "2026-11-13T20:00:00Z"; // 上海 11-14 04:00
    const later = Date.parse("2026-11-14T02:00:00Z"); // 上海 11-14 10:00，UTC 11-14 02:00
    expect(formatRelativeTime(value, later, "Asia/Shanghai")).toBe("6 小时前");
    expect(formatRelativeTime(value, later, "UTC")).toBe("昨天");
  });

  it("无法解析时显示未知占位", () => {
    expect(formatRelativeTime("不是时间", now)).toBe(UNKNOWN_TIME);
  });
});
