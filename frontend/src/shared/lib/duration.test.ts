import { describe, expect, it } from "vitest";
import { formatDuration, UNKNOWN_DURATION } from "./duration";

describe("formatDuration", () => {
  it("不足一小时写 m:ss，一小时以上写 h:mm:ss", () => {
    expect(formatDuration(0)).toBe("0:00");
    expect(formatDuration(59)).toBe("0:59");
    expect(formatDuration(240)).toBe("4:00");
    expect(formatDuration(3599)).toBe("59:59");
    expect(formatDuration(3600)).toBe("1:00:00");
    expect(formatDuration(3725)).toBe("1:02:05");
    expect(formatDuration(100 * 3600 + 61)).toBe("100:01:01");
  });

  it("小数秒四舍五入到整秒", () => {
    expect(formatDuration(239.6)).toBe("4:00");
    expect(formatDuration(239.4)).toBe("3:59");
  });

  it("未知时长写占位，不写 0:00", () => {
    expect(formatDuration(null)).toBe(UNKNOWN_DURATION);
  });
});
