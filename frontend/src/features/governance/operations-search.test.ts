import { describe, expect, it } from "vitest";
import { parseOperationsSearch } from "./operations-search";

const id = "0192E3A4-0000-4000-8000-0000000000AB";

describe("parseOperationsSearch", () => {
  it("没有参数时都是 undefined（显式写出，路由才会丢掉原来的值）", () => {
    const parsed = parseOperationsSearch({});
    expect(parsed).toEqual({ page: undefined, release: undefined });
    expect("page" in parsed && "release" in parsed).toBe(true);
  });

  it("页码是 2 及以上的整数；第 1 页不写进地址，不合法的值丢弃", () => {
    expect(parseOperationsSearch({ page: 3 }).page).toBe(3);
    expect(parseOperationsSearch({ page: "04" }).page).toBe(4);
    for (const page of [1, 0, -2, 2.5, "abc", "1e3", null, true]) expect(parseOperationsSearch({ page }).page, String(page)).toBeUndefined();
  });

  it("release 只保留 UUID 并统一成小写；不是 UUID 时看全部记录", () => {
    expect(parseOperationsSearch({ release: id }).release).toBe(id.toLowerCase());
    for (const release of ["", "abc", "0192e3a4-0000-4000-8000", 7, ["x"], `${id}x`]) expect(parseOperationsSearch({ release }).release, String(release)).toBeUndefined();
  });
});
