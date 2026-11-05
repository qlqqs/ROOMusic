import { describe, expect, it } from "vitest";
import { artistIdOf, parseArtistDetailSearch, parseArtistsSearch } from "./artist-search";

const listDefaults = { q: undefined, initial: undefined, role: undefined, sort: undefined, page: undefined };
const detailDefaults = { albums_page: undefined, track_role: undefined, tracks_page: undefined, names_page: undefined };
const invalidPages = [undefined, null, "", "bad", "1.5", "-2", " 2 ", [], {}, 0, 1, -1, 1.5, Infinity, NaN, 1_000_001];

describe("艺人列表URL校验", () => {
  it("显式清理未知与非法状态，不把路由原始输入留在结果里", () => {
    expect(parseArtistsSearch({ q: [], initial: "lower", role: "singer", sort: "random", page: "bad", user_id: "other" })).toStrictEqual(listDefaults);
    expect(parseArtistsSearch({})).toStrictEqual(listDefaults);
  });
  it("只trim用户写法，保留角色和独立页码，不自行规范化艺人名字", () => {
    expect(parseArtistsSearch({ q: "  Ｂeyoncé 周杰倫  ", initial: "B", role: "composer", sort: "name", page: "02" })).toStrictEqual({
      q: "Ｂeyoncé 周杰倫", initial: "B", role: "composer", sort: undefined, page: 2,
    });
    expect(parseArtistsSearch({ q: 1984 }).q).toBe("1984");
  });
  it.each([..."ABCDEFGHIJKLMNOPQRSTUVWXYZ", "kana", "#"])("按名字排序保留首字母%s，按专辑数排序丢弃首字母", (initial) => {
    expect(parseArtistsSearch({ initial }).initial).toBe(initial);
    expect(parseArtistsSearch({ initial, sort: "album_count", role: "producer", page: 4 })).toStrictEqual({
      ...listDefaults, sort: "album_count", role: "producer", page: 4,
    });
  });
  it.each(["release_artist", "album_artist", "track_artist", "composer", "conductor", "performer", "producer"])("列表接受角色%s", (role) => {
    expect(parseArtistsSearch({ role }).role).toBe(role);
  });
  it.each([null, "a", "Kana", "汉字", 1, ["A"]])("非法首字母%j归一", (initial) => {
    expect(parseArtistsSearch({ initial, role: "composer" })).toStrictEqual({ ...listDefaults, role: "composer" });
  });
  it.each(["a".repeat(200), "爱".repeat(66) + "ab", "🎵".repeat(50)])("查询按200字节边界而非字符数限制", (q) => {
    expect(parseArtistsSearch({ q: ` ${q} ` }).q).toBe(q);
    expect(parseArtistsSearch({ q: `${q}a` }).q).toBeUndefined();
  });
  it.each([null, undefined, " ", "\n　", true, [], {}, Infinity, NaN])("空白/非法查询%j清除但保留筛选", (q) => {
    expect(parseArtistsSearch({ q, initial: "Z", role: "conductor", page: 3 })).toStrictEqual({ ...listDefaults, initial: "Z", role: "conductor", page: 3 });
  });
  it.each(invalidPages)("非法/默认页码%j归一，不抹掉筛选", (page) => {
    expect(parseArtistsSearch({ q: "爱", initial: "A", role: "performer", page })).toStrictEqual({ ...listDefaults, q: "爱", initial: "A", role: "performer" });
  });
  it.each([2, "02", 1_000_000])("接受有效页码%j", (page) => {
    expect(parseArtistsSearch({ page }).page).toBe(Number(page));
  });
});

describe("艺人详情URL分页互不串扰", () => {
  const valid = { albums_page: 2, tracks_page: 3, names_page: 4, track_role: "composer" };
  it("分别恢复专辑、曲目、名字页码和角色，忽略其他feature参数", () => {
    expect(parseArtistDetailSearch({ albums_page: "02", tracks_page: 3, names_page: "04", track_role: "composer", page: 88, artists_page: 99 })).toStrictEqual(valid);
    expect(parseArtistDetailSearch({})).toStrictEqual(detailDefaults);
  });
  it.each(["albums_page", "tracks_page", "names_page"] as const)("%s清理或翻页不改另两栏及角色", (field) => {
    for (const page of invalidPages) expect(parseArtistDetailSearch({ ...valid, [field]: page })).toStrictEqual({ ...valid, [field]: undefined });
    expect(parseArtistDetailSearch({ ...valid, [field]: "05" })).toStrictEqual({ ...valid, [field]: 5 });
    expect(parseArtistDetailSearch({ ...valid, [field]: 1_000_000 })).toStrictEqual({ ...valid, [field]: 1_000_000 });
  });
  it.each(["track_artist", "composer", "conductor", "performer", "producer"])("曲目栏接受%s，不影响三栏页码", (role) => {
    expect(parseArtistDetailSearch({ ...valid, track_role: role })).toStrictEqual({ ...valid, track_role: role });
  });
  it.each(["release_artist", "album_artist", "singer", "COMPOSER", "", null, ["composer"]])("曲目栏拒绝%j而保留分页", (role) => {
    expect(parseArtistDetailSearch({ ...valid, track_role: role })).toStrictEqual({ ...valid, track_role: undefined });
  });
  it("重复校验幂等，不修改传入对象，复制地址可独立恢复", () => {
    const raw = { albums_page: "02", tracks_page: "03", names_page: "04", track_role: "composer" };
    const parsed = parseArtistDetailSearch(raw);
    expect(parseArtistDetailSearch(parsed)).toStrictEqual(parsed);
    expect(parseArtistDetailSearch(Object.fromEntries(new URLSearchParams(raw)))).toStrictEqual(valid);
    expect(raw).toStrictEqual({ albums_page: "02", tracks_page: "03", names_page: "04", track_role: "composer" });
  });
});

describe("艺人路径身份", () => {
  it("UUID不猜身份，只统一大小写", () => {
    expect(artistIdOf("ABCDEF00-1234-5678-9012-ABCDEF123456")).toBe("abcdef00-1234-5678-9012-abcdef123456");
  });
  it.each(["", "artist", "周杰伦", "00000000000040008000000000000001", "00000000-0000-4000-8000-00000000000g", " 00000000-0000-4000-8000-000000000001 ", "../artists/1", "a".repeat(256)])("非法ID %s不进入请求", (value) => {
    expect(artistIdOf(value)).toBeNull();
  });
});
