import { describe, expect, it } from "vitest";
import { ACTIVE_TOLERANCE_MS, activeGroupIndex, followIndex, groupSyncedLines } from "./lyrics";

const line = (start_ms: number, text: string) => ({ start_ms, text });

describe("groupSyncedLines", () => {
  it("没有行就没有组", () => {
    expect(groupSyncedLines([])).toEqual([]);
  });

  it("每个开始时间一组，同一开始时间的多行按原顺序并成一组（主行在前，对照行在后）", () => {
    expect(groupSyncedLines([line(1000, "one"), line(5000, "English"), line(5000, "中文"), line(5000, "third"), line(9000, "last")])).toEqual([
      { startMs: 1000, lines: ["one"] },
      { startMs: 5000, lines: ["English", "中文", "third"] },
      { startMs: 9000, lines: ["last"] },
    ]);
  });

  it("空文本是间奏：保留为没有文字的组；同一时间的空行不进入对照行", () => {
    expect(groupSyncedLines([line(0, ""), line(1000, "a"), line(1000, ""), line(2000, ""), line(2000, "")])).toEqual([
      { startMs: 0, lines: [] },
      { startMs: 1000, lines: ["a"] },
      { startMs: 2000, lines: [] },
    ]);
  });

  it("不修改传入的行", () => {
    const input = [line(1000, "a"), line(1000, "b")];
    const snapshot = structuredClone(input);
    groupSyncedLines(input);
    expect(input).toEqual(snapshot);
  });
});

describe("activeGroupIndex", () => {
  const groups = [{ startMs: 5000 }, { startMs: 10000 }, { startMs: 20000 }];

  it("没有组时是 -1", () => {
    expect(activeGroupIndex([], 0)).toBe(-1);
    expect(activeGroupIndex([], 12345)).toBe(-1);
  });

  it("位置在第一组之前是 -1，到达开始时间的那一刻轮到该组", () => {
    expect(activeGroupIndex(groups, 0)).toBe(-1);
    expect(activeGroupIndex(groups, 4000)).toBe(-1);
    expect(activeGroupIndex(groups, 5000)).toBe(0);
    expect(activeGroupIndex(groups, 9999 - ACTIVE_TOLERANCE_MS)).toBe(0);
    expect(activeGroupIndex(groups, 10000)).toBe(1);
    expect(activeGroupIndex(groups, 19999 - ACTIVE_TOLERANCE_MS)).toBe(1);
    expect(activeGroupIndex(groups, 20000)).toBe(2);
    expect(activeGroupIndex(groups, 3_600_000)).toBe(2);
  });

  it("容差让点击一行后略早于该行的位置也算轮到它，超出容差则不算；容差只有几十毫秒", () => {
    expect(ACTIVE_TOLERANCE_MS).toBeGreaterThan(0);
    expect(ACTIVE_TOLERANCE_MS).toBeLessThanOrEqual(100);
    expect(activeGroupIndex(groups, 10000 - ACTIVE_TOLERANCE_MS)).toBe(1);
    expect(activeGroupIndex(groups, 10000 - ACTIVE_TOLERANCE_MS - 1)).toBe(0);
    expect(activeGroupIndex(groups, 5000 - ACTIVE_TOLERANCE_MS)).toBe(0);
    expect(activeGroupIndex(groups, 5000 - ACTIVE_TOLERANCE_MS - 1)).toBe(-1);
  });

  it("与线性查找在两千组、各种位置上结果一致", () => {
    // 确定性的伪随机数：开始时间严格递增，间隔 1 到 30 秒。
    let seed = 12345;
    const next = () => {
      seed = (seed * 1103515245 + 12345) & 0x7fffffff;
      return seed;
    };
    let start = 0;
    const many = Array.from({ length: 2000 }, () => ({ startMs: (start += 1000 + (next() % 29000)) }));
    for (let probe = 0; probe < 5000; probe += 1) {
      const position = next() % (start + 10_000);
      let expected = -1;
      many.forEach((group, index) => {
        if (group.startMs <= position + ACTIVE_TOLERANCE_MS) expected = index;
      });
      expect(activeGroupIndex(many, position)).toBe(expected);
    }
  });
});

describe("followIndex", () => {
  const groups = [{ lines: [] }, { lines: ["a"] }, { lines: [] }, { lines: [] }, { lines: ["b", "译"] }, { lines: [] }];

  it("当前组有文字时跟随它", () => {
    expect(followIndex(groups, 1)).toBe(1);
    expect(followIndex(groups, 4)).toBe(4);
  });

  it("当前组是间奏时停在最近的上一句", () => {
    expect(followIndex(groups, 2)).toBe(1);
    expect(followIndex(groups, 3)).toBe(1);
    expect(followIndex(groups, 5)).toBe(4);
  });

  it("还没有任何一句、或位置在第一组之前，没有可跟随的组", () => {
    expect(followIndex(groups, -1)).toBe(-1);
    expect(followIndex(groups, 0)).toBe(-1);
    expect(followIndex([], 0)).toBe(-1);
  });

  it("下标越界时按最后一组处理", () => {
    expect(followIndex(groups, 99)).toBe(4);
  });
});
