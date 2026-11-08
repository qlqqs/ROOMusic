import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue } from "../../../test/mutate";
import { decodeScan, decodeScanSchedule } from "./decoders";

const schedule = sampleBody("scan-schedule/get");
const scan = sampleBody("scans/get-succeeded");

describe("定时扫描设置解码", () => {
  it.each([
    { enabled: true, hour: 4 },
    { enabled: false, hour: 5 },
  ])("保留开关 $enabled 与整点 $hour", ({ enabled, hour }) => {
    const input = withValue(withValue(schedule, ["enabled"], enabled), ["hour"], hour);
    expect(decodeScanSchedule(input)).toEqual({ enabled, hour });
  });

  it("拒绝范围之外的整点", () => {
    expect(() => decodeScanSchedule(withValue(schedule, ["hour"], 24))).toThrow(d.DecodeError);
  });
});

describe("扫描发起方解码", () => {
  it.each(["manual", "scheduled"])("保留发起方 %s", (trigger) => {
    expect(decodeScan(withValue(scan, ["trigger"], trigger)).trigger).toBe(trigger);
  });

  it("拒绝未知发起方", () => {
    expect(() => decodeScan(withValue(scan, ["trigger"], "automatic"))).toThrow(d.DecodeError);
  });
});
