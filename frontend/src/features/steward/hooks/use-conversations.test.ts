import { describe, expect, it } from "vitest";
import { BUSY_POLL_MS, busyPollInterval } from "./use-conversations";

describe("“正在别处回答”的轮询", () => {
  it("有不是本页发起的运行时每 3 秒重读", () => {
    expect(busyPollInterval(null, true)).toBe(BUSY_POLL_MS);
    expect(busyPollInterval(null, false)).toBe(false);
  });

  it("上一次读取出错（404、403 等）时停止", () => {
    expect(busyPollInterval(new Error("404"), true)).toBe(false);
  });
});
