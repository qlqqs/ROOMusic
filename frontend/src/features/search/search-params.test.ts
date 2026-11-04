import { describe, expect, it } from "vitest";
import { draftQueryOf, parseSearchPageSearch, searchPageFor } from "./search-params";

describe("搜索地址与草稿", () => {
  it("保留原样查询、分别恢复两栏页码，不启用艺人页码", () => {
    expect(parseSearchPageSearch({ q: "  Ｂeyoncé 周杰倫  ", albums_page: "02", tracks_page: 21, artists_page: 3 })).toEqual({
      q: "Ｂeyoncé 周杰倫", albums_page: 2, tracks_page: 21,
    });
  });

  it.each([undefined, null, "", "bad", "1.5", "-2", 0, 1, 1.5, Infinity, 1_000_001])("非法或默认页码 %s 归一，不影响另一栏", (page) => {
    expect(parseSearchPageSearch({ q: "爱", albums_page: page, tracks_page: 4 })).toEqual({ q: "爱", albums_page: undefined, tracks_page: 4 });
    expect(parseSearchPageSearch({ q: "爱", albums_page: 4, tracks_page: page })).toEqual({ q: "爱", albums_page: 4, tracks_page: undefined });
  });

  it("换词清除两栏旧页码，保留用户写法而非自行规范化", () => {
    expect(searchPageFor("Beyoncé")).toEqual({ q: "Beyoncé", albums_page: undefined, tracks_page: undefined });
  });

  it("空白不查，单字可查；长度按 UTF-8 字节而非字符数限制", () => {
    expect(draftQueryOf(" \n　 ")).toEqual({ kind: "empty" });
    expect(draftQueryOf(" 爱 ")).toEqual({ kind: "ready", q: "爱" });
    for (const q of ["a".repeat(200), "爱".repeat(66) + "ab", "🎵".repeat(50)]) {
      expect(draftQueryOf(` ${q} `)).toEqual({ kind: "ready", q });
      expect(parseSearchPageSearch({ q }).q).toBe(q);
      expect(draftQueryOf(q + "a")).toEqual({ kind: "too_long" });
      expect(parseSearchPageSearch({ q: q + "a" }).q).toBeUndefined();
    }
  });
});
