import { describe, expect, it } from "vitest";
import { isFiltered, MAX_SEARCH_BYTES, parseAlbumSearch, parseSearchText } from "./album-search";

describe("parseSearchText", () => {
  it("去掉首尾空白后保留", () => {
    expect(parseSearchText("  贝多芬 ")).toBe("贝多芬");
    expect(parseSearchText("Goldberg Variations")).toBe("Goldberg Variations");
  });

  it("空白、空字符串与非文本一律丢弃", () => {
    for (const value of ["", "   ", "\t\n", undefined, null, true, false, {}, [], ["a"]]) {
      expect(parseSearchText(value)).toBeUndefined();
    }
  });

  it("路由把形如数字的值解析成数字时，按原文本处理（?q=1984）；非有限数字丢弃", () => {
    expect(parseSearchText(1984)).toBe("1984");
    expect(parseSearchText(0)).toBe("0");
    expect(parseSearchText(Number.NaN)).toBeUndefined();
    expect(parseSearchText(Number.POSITIVE_INFINITY)).toBeUndefined();
  });

  it("上限是 200 个 UTF-8 字节，不是字符数：200 个字母可以，67 个汉字（201 字节）不行", () => {
    expect(MAX_SEARCH_BYTES).toBe(200);
    expect(parseSearchText("a".repeat(200))).toHaveLength(200);
    expect(parseSearchText("a".repeat(201))).toBeUndefined();
    expect(parseSearchText("长".repeat(66))).toHaveLength(66);
    expect(parseSearchText("长".repeat(67))).toBeUndefined();
  });

  it("上限按去空白之后的字节数计", () => {
    expect(parseSearchText(`  ${"a".repeat(200)}  `)).toHaveLength(200);
  });
});

describe("parseAlbumSearch", () => {
  it("合法的值原样保留", () => {
    expect(parseAlbumSearch({ q: "bach", attention: "required", page: 3 })).toEqual({ q: "bach", attention: "required", page: 3 });
    expect(parseAlbumSearch({ page: "04" }).page).toBe(4);
  });

  it("不合法的值显式返回 undefined（只省略键的话，路由会把原值留在 search 里）", () => {
    const parsed = parseAlbumSearch({ q: "  ", attention: "yes", page: 0 });
    expect(parsed).toEqual({ q: undefined, attention: undefined, page: undefined });
    for (const key of ["q", "attention", "page"] as const) expect(key in parsed).toBe(true);
  });

  it("attention 只有 required 保留；第 1 页不保留页码；未知参数被丢弃", () => {
    expect(parseAlbumSearch({ attention: "Required" }).attention).toBeUndefined();
    expect(parseAlbumSearch({ attention: true }).attention).toBeUndefined();
    expect(parseAlbumSearch({ page: 1 }).page).toBeUndefined();
    expect(parseAlbumSearch({ page: -2 }).page).toBeUndefined();
    expect(parseAlbumSearch({ page: 2.5 }).page).toBeUndefined();
    expect(Object.keys(parseAlbumSearch({ other: "x", release: "y" })).sort()).toEqual(["attention", "page", "q"]);
  });
});

describe("isFiltered", () => {
  it("有搜索词或筛选才算带条件；页码不算", () => {
    expect(isFiltered({})).toBe(false);
    expect(isFiltered({ page: 3 })).toBe(false);
    expect(isFiltered({ q: "bach" })).toBe(true);
    expect(isFiltered({ attention: "required" })).toBe(true);
  });
});
