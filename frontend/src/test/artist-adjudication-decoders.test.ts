import { describe, expect, it } from "vitest";
import { decodeArtistAlbumsPage, decodeArtistPage, decodeArtistParticipationAlbumsPage } from "../features/artists/api/decoders";
import { decodeArtistMergedSources } from "../features/governance/api/decoders";
import { sampleBody } from "./api-samples";
import { without, withValue } from "./mutate";

const body = (name: string) => sampleBody(`artists/adjudication/${name}`);

describe("I1 恢复来源直接产品 decoder", () => {
  it("没有来源与恢复之后保留空列表，仍有来源但键已移走时保留空键数组", () => {
    expect(decodeArtistMergedSources(body("merged-sources-empty"))).toEqual({ items: [] });
    expect(decodeArtistMergedSources(body("merged-sources-after-restore"))).toEqual({ items: [] });
    const decoded = decodeArtistMergedSources(body("merged-sources-empty-keys"));
    expect(decoded).toEqual(body("merged-sources-empty-keys"));
    expect(decoded.items).toHaveLength(1);
    expect(decoded.items[0]?.name_key_ids).toEqual([]);
  });
  // 畸形反例仅由真实响应派生；不替换正式样本，也不增加测试专用 decoder。
  it.each(["artist_id", "name", "revision", "name_key_ids", "operation_id", "merged_at"])("缺少 %s 时拒绝整个响应", (key) => {
    expect(() => decodeArtistMergedSources(without(body("merged-sources"), ["items", 0, key]))).toThrow();
  });
  it.each([
    ["artist_id", "bad"], ["revision", 0], ["name_key_ids", null],
    ["name_key_ids", ["bad"]], ["operation_id", "bad"], ["merged_at", "not-a-date"],
  ])("%s 形状非法时拒绝整个响应", (key, value) => {
    if (typeof key !== "string") throw new Error("反例必须指定字段名");
    expect(() => decodeArtistMergedSources(withValue(body("merged-sources"), ["items", 0, key], value))).toThrow();
  });
});

describe("I2 任意参与和默认专辑使用不同产品 decoder", () => {
  it("any 保留角色和非参与代表，不能用代表版本覆盖实际署名版本", () => {
    const decoded = decodeArtistParticipationAlbumsPage(body("albums-any"));
    expect(decoded).toEqual(body("albums-any"));
    expect(decoded.items.map((item) => item.roles)).toEqual([["release_artist", "album_artist", "track_artist"], ["composer", "performer"]]);
    expect(decoded.items[0]?.credited_release_id).not.toBe(decoded.items[0]?.group.representative.id);
  });
  it("默认读取在 any 之后仍不增加 roles，不能冒充任意参与响应", () => {
    for (const name of ["albums-default", "albums-default-after-any"]) {
      const decoded = decodeArtistAlbumsPage(body(name));
      expect(decoded).toEqual(body(name));
      expect(decoded.items).toHaveLength(1);
      expect(decoded.items[0]).not.toHaveProperty("roles");
      expect(() => decodeArtistParticipationAlbumsPage(body(name))).toThrow();
    }
  });
  it.each([{ roles: [] }, { roles: null }, { roles: ["unknown_role"] }])("任意参与不能接受空、null 或未知角色：$roles", ({ roles }) => {
    expect(() => decodeArtistParticipationAlbumsPage(withValue(body("albums-any"), ["items", 0, "roles"], roles))).toThrow();
  });
  it("任意参与的角色字段缺失时拒绝", () => {
    expect(() => decodeArtistParticipationAlbumsPage(without(body("albums-any"), ["items", 0, "roles"]))).toThrow();
  });
});

describe("I6 发行级角色筛选不改 DTO 词表", () => {
  it("专辑艺人筛选涵盖两种发行级身份，release_artist 仍精确筛选", () => {
    const union = decodeArtistPage(body("role-album-artist"));
    const exact = decodeArtistPage(body("role-release-artist"));
    expect(union).toEqual(body("role-album-artist"));
    expect(exact).toEqual(body("role-release-artist"));
    expect(union.items).toHaveLength(2);
    expect(exact.items).toHaveLength(1);
    expect(union.items.some((item) => item.roles.includes("album_artist") && !item.roles.includes("release_artist"))).toBe(true);
    expect(exact.items.every((item) => item.roles.includes("release_artist"))).toBe(true);
  });
});
