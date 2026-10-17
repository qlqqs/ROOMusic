import { describe, expect, it } from "vitest";
import { parseIssuesSearch } from "./issues-search";

const id = "0192E3A4-0000-4000-8000-0000000000AB";

describe("parseIssuesSearch", () => {
  it("没有参数时都是 undefined（显式写出，路由才会丢掉原来的值）", () => {
    const parsed = parseIssuesSearch({});
    expect(parsed).toEqual({ status: undefined, category: undefined, page: undefined, release: undefined });
    expect(["status", "category", "page", "release"].every((key) => key in parsed)).toBe(true);
  });

  it("status 只保留 reviewed：待处理是默认值，不写进地址；其他值丢弃", () => {
    expect(parseIssuesSearch({ status: "reviewed" }).status).toBe("reviewed");
    for (const status of ["open", "", "REVIEWED", 1, ["reviewed"]]) expect(parseIssuesSearch({ status }).status, String(status)).toBeUndefined();
  });

  it("category 只保留三种类别", () => {
    for (const category of ["fields", "grouping", "missing_tracks"] as const) expect(parseIssuesSearch({ category }).category).toBe(category);
    for (const category of ["", "artwork", "Fields", 3, ["fields"]]) expect(parseIssuesSearch({ category }).category, String(category)).toBeUndefined();
  });

  it("页码是 2 及以上的整数；release 只保留 UUID 并统一成小写", () => {
    expect(parseIssuesSearch({ page: 3, release: id })).toEqual({ status: undefined, category: undefined, page: 3, release: id.toLowerCase() });
    for (const page of [1, 0, -2, 2.5, "abc"]) expect(parseIssuesSearch({ page }).page, String(page)).toBeUndefined();
    for (const release of ["", "abc", 7, `${id}x`]) expect(parseIssuesSearch({ release }).release, String(release)).toBeUndefined();
  });
});
