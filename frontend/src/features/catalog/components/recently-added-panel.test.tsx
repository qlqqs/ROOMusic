import { describe, expect, it } from "vitest";
import { recentlyAddedVisibleCount } from "./recently-added-panel";

describe.each([
  { columns: 1, limit: 2 },
  { columns: 2, limit: 4 },
  { columns: 3, limit: 3 },
  { columns: 6, limit: 6 },
  { columns: 8, limit: 8 },
  { columns: 10, limit: 8 },
  { columns: null, limit: 8 },
])("最近新增张数：columns=$columns，上限 $limit", ({ columns, limit }) => {
  it("没有数据时不补足卡片", () => {
    expect(recentlyAddedVisibleCount(columns, 0)).toBe(0);
  });

  it("数据不足时只显示实际张数", () => {
    expect(recentlyAddedVisibleCount(columns, limit - 1)).toBe(limit - 1);
  });

  it("数据恰好达到上限时全部显示", () => {
    expect(recentlyAddedVisibleCount(columns, limit)).toBe(limit);
  });

  it("数据超过可显示张数时截取，任何列数都不超过八张", () => {
    expect(recentlyAddedVisibleCount(columns, limit + 1)).toBe(limit);
    expect(recentlyAddedVisibleCount(columns, 20)).toBe(limit);
  });
});
