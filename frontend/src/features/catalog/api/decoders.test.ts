import { describe, expect, it } from "vitest";
import { decode as d } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { withValue, without } from "../../../test/mutate";
import { decodeReleaseGroupPage } from "./decoders";

describe("decodeReleaseGroupPage", () => {
  const page = sampleBody("release-groups/list-one-with-artwork");

  it("解码空列表与单项列表，总数在 pagination.total", () => {
    const empty = decodeReleaseGroupPage(sampleBody("release-groups/list-empty"));
    expect(empty.items).toEqual([]);
    expect(empty.pagination).toEqual({ page: 1, page_size: 1, total: 0 });
    const one = decodeReleaseGroupPage(sampleBody("release-groups/list-one"));
    expect(one.pagination.total).toBe(1);
    expect(one.items[0]?.representative.artwork).toBeNull();
  });

  it("解码带封面的代表版本", () => {
    const [group] = decodeReleaseGroupPage(page).items;
    expect(group?.release_count).toBe(1);
    expect(group?.representative).toMatchObject({
      title: "Sample Preludes",
      artist: "Sample Pianist",
      year: 2001,
      artwork: { mime: "image/webp", width: 640, height: 480 },
    });
  });

  it("每页最多 100 个组", () => {
    const first = (page as { items: unknown[] }).items[0];
    const many = (count: number) => withValue(page, ["items"], Array.from({ length: count }, () => structuredClone(first)));
    expect(decodeReleaseGroupPage(many(100)).items).toHaveLength(100);
    expect(() => decodeReleaseGroupPage(many(101))).toThrow(d.DecodeError);
  });

  it.each([
    ["缺少 pagination", without(page, ["pagination"])],
    ["总数为负", withValue(page, ["pagination", "total"], -1)],
    ["page_size 超过 100", withValue(page, ["pagination", "page_size"], 101)],
    ["page 为 0", withValue(page, ["pagination", "page"], 0)],
    ["组的版本数为 0", withValue(page, ["items", 0, "release_count"], 0)],
    ["缺少代表版本", without(page, ["items", 0, "representative"])],
    ["代表版本缺少标题", without(page, ["items", 0, "representative", "title"])],
    ["年份为 0", withValue(page, ["items", 0, "representative", "year"], 0)],
    ["年份是字符串", withValue(page, ["items", 0, "representative", "year"], "2001")],
    ["封面缺少宽度", without(page, ["items", 0, "representative", "artwork", "width"])],
    ["封面高度为 0", withValue(page, ["items", 0, "representative", "artwork", "height"], 0)],
    ["attention_count 为负", withValue(page, ["items", 0, "attention_count"], -1)],
    ["组 id 不是 UUID", withValue(page, ["items", 0, "id"], "group-1")],
  ])("拒绝：%s", (_name, input) => {
    expect(() => decodeReleaseGroupPage(input)).toThrow(d.DecodeError);
  });
});
