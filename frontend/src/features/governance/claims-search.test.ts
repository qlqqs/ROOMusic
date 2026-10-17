import { describe, expect, it } from "vitest";
import { parseClaimsSearch } from "./claims-search";

const id = "0192E3A4-0000-4000-8000-0000000000AB";

describe("parseClaimsSearch", () => {
  it("没有参数时都是 undefined（显式写出，路由才会丢掉原来的值）", () => {
    const parsed = parseClaimsSearch({});
    expect(parsed).toEqual({ page: undefined, release: undefined, target: undefined });
    expect(["page", "release", "target"].every((key) => key in parsed)).toBe(true);
  });

  it("页码是 2 及以上的整数；第 1 页是默认值，不写进地址", () => {
    expect(parseClaimsSearch({ page: 3 }).page).toBe(3);
    for (const page of [1, 0, -2, 2.5, "abc", ["2"]]) expect(parseClaimsSearch({ page }).page, String(page)).toBeUndefined();
  });

  it("release 只保留 UUID 并统一成小写，其余丢弃", () => {
    expect(parseClaimsSearch({ release: id }).release).toBe(id.toLowerCase());
    for (const release of ["", "abc", 7, `${id}x`, [id]]) expect(parseClaimsSearch({ release }).release, String(release)).toBeUndefined();
  });
});

describe("parseClaimsSearch：target", () => {
  const target = "0192E3A4-0000-4000-8000-0000000000CD";

  it("选中了旧版本时保留 UUID 形状的 target，统一成小写", () => {
    expect(parseClaimsSearch({ release: id, target })).toEqual({ page: undefined, release: id.toLowerCase(), target: target.toLowerCase() });
    for (const bad of ["", "abc", 7, `${target}x`, [target]]) expect(parseClaimsSearch({ release: id, target: bad }).target, String(bad)).toBeUndefined();
  });

  it("没有选中旧版本时 target 没有意义，丢弃", () => {
    expect(parseClaimsSearch({ target }).target).toBeUndefined();
    expect(parseClaimsSearch({ release: "abc", target }).target).toBeUndefined();
  });
});
