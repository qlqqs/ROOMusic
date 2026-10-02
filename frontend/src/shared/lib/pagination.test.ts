import { describe, expect, it } from "vitest";
import { pageCountOf, parsePageParam } from "./pagination";

describe("parsePageParam", () => {
  it("接受 2 及以上的整数，数字与纯数字字符串都可以", () => {
    expect(parsePageParam(2)).toBe(2);
    expect(parsePageParam(37)).toBe(37);
    expect(parsePageParam("5")).toBe(5);
    expect(parsePageParam("03")).toBe(3);
    expect(parsePageParam(1_000_000)).toBe(1_000_000);
  });

  it.each([
    ["第 1 页是默认值，不保留", 1],
    ["0", 0],
    ["负数", -2],
    ["小数", 2.5],
    ["过大", 1_000_001],
    ["Infinity", Number.POSITIVE_INFINITY],
    ["NaN", Number.NaN],
    ["非数字字符串", "abc"],
    ["带符号的字符串", "-3"],
    ["小数字符串", "2.5"],
    ["空字符串", ""],
    ["布尔值", true],
    ["对象", {}],
    ["数组", [3]],
    ["null", null],
    ["undefined", undefined],
  ])("拒绝：%s", (_name, value) => {
    expect(parsePageParam(value)).toBeUndefined();
  });
});

describe("pageCountOf", () => {
  it("向上取整；没有数据时为 0", () => {
    expect(pageCountOf(0, 20)).toBe(0);
    expect(pageCountOf(1, 20)).toBe(1);
    expect(pageCountOf(20, 20)).toBe(1);
    expect(pageCountOf(21, 20)).toBe(2);
    expect(pageCountOf(95, 20)).toBe(5);
  });

  it("页大小不合法时不除零", () => {
    expect(pageCountOf(10, 0)).toBe(0);
  });
});
