import { describe, expect, it } from "vitest";
import type { MatchField, MatchRole, SearchMatchDTO } from "./api/decoders";
import { matchNote } from "./match-note";

function match(overrides: Partial<SearchMatchDTO> = {}): SearchMatchDTO {
  return { kind: "contains", field: "credit", role: "composer", original: "久石譲", artist_id: null, alias: null, release_id: null, track_id: null, ...overrides };
}

describe("匹配说明", () => {
  it.each<{ role: MatchRole; label: string }>([
    { role: "composer", label: "作曲" }, { role: "conductor", label: "指挥" },
    { role: "performer", label: "演奏" }, { role: "producer", label: "制作人" },
  ])("$role 显示角色与原文，不自行规范化", ({ role, label }) => {
    expect(matchNote(match({ role }), ["Summer"])).toBe(`${label}：久石譲`);
  });

  it.each<{ field: MatchField; original: string; label: string }>([
    { field: "label", original: "音樂廠牌", label: "厂牌" },
    { field: "catalog_number", original: "ＳＲＣＬ-1234", label: "编号" },
    { field: "track_artist", original: "Beyoncé", label: "曲目艺人" },
    { field: "release_title", original: "ＡＢＣ 愛", label: "专辑" },
  ])("$field 保留实际命中的原文", ({ field, original, label }) => {
    expect(matchNote(match({ field, original, role: null }), [])).toBe(`${label}：${original}`);
  });

  it.each<MatchField>(["release_title", "track_title", "release_artist", "album_artist", "track_artist", "credit", "label", "catalog_number"])(
    "%s 已显示原文时不再重复", (field) => {
      expect(matchNote(match({ field }), ["Summer", "久石譲", "电影原声"])).toBeNull();
    },
  );

  it("另一个版本的标题命中仍说明原文，不因字段是标题就丢掉", () => {
    expect(matchNote(match({ field: "release_title", original: "ＡＢＣ 愛", role: null }), ["原版专辑", "原版艺人"])).toBe("专辑：ＡＢＣ 愛");
  });

  it.each(["", " \t\n "])("没有可显示原文时不生成空说明（%j）", (original) => {
    expect(matchNote(match({ original }), [])).toBeNull();
  });

  it("别名优先显示实际 alias，角色不能把别名误写成艺人名", () => {
    expect(matchNote(match({ field: "artist_alias", role: "track_artist", original: "邓丽君", alias: "Teresa Teng" }), ["邓丽君"])).toBe("别名：Teresa Teng");
    expect(matchNote(match({ field: "artist_alias", original: "Teresa Teng", alias: null }), [])).toBe("别名：Teresa Teng");
  });
});
