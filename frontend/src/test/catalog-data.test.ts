import { describe, expect, it } from "vitest";
import { decodeRecentlyAdded, decodeReleaseGroupAddedPage, decodeReleaseGroupPage } from "../features/catalog/api/decoders";
import { albumGroup, albumGroups, respondToGroupList, respondToRecentlyAdded } from "./catalog-data";

const query = (value: string) => new URLSearchParams(value);

describe("扫描反馈的共用专辑接口替身", () => {
  const groups = [
    albumGroup(1, { title: "Album A", added_at: "2026-11-02T00:00:00Z", attention_count: 1 }),
    albumGroup(2, { title: "Album B", added_at: "2026-11-03T00:00:00Z", attention_count: 1 }),
    albumGroup(3, { title: "Album C", added_at: "2026-11-03T00:00:00Z", attention_count: 1 }),
    albumGroup(4, { title: "另一张专辑", added_at: "2026-11-04T00:00:00Z" }),
  ];

  it("按加入时间倒序，同时间按 ID 倒序；不改写夹具原顺序", () => {
    const before = structuredClone(groups);
    const result = respondToGroupList(groups, query("sort=added_at_desc"));
    expect(result.status).toBe(200);
    expect(decodeReleaseGroupAddedPage(result.body).items.map((group) => group.id)).toEqual([groups[3]?.id, groups[2]?.id, groups[1]?.id, groups[0]?.id]);
    expect(groups).toEqual(before);
  });

  it("先按时间排序再对筛选结果分页，保留正确总数", () => {
    // 两个干扰项分别只满足搜索或关注，避免漏掉任一筛选仍能通过。
    const candidates = [
      ...groups,
      albumGroup(5, { title: "Album D", added_at: "2026-11-05T00:00:00Z" }),
      albumGroup(6, { title: "搜索之外", added_at: "2026-11-06T00:00:00Z", attention_count: 1 }),
    ];
    const result = respondToGroupList(candidates, query("sort=added_at_desc&q=Album&attention=required&page=2&page_size=2"));
    expect(decodeReleaseGroupAddedPage(result.body)).toMatchObject({ items: [groups[0]], pagination: { page: 2, page_size: 2, total: 3 } });
  });

  it("默认列表保留夹具预先指定的服务器顺序", () => {
    const reversed = [...groups].reverse();
    expect(decodeReleaseGroupPage(respondToGroupList(reversed, query("")).body).items).toEqual(reversed);
  });

  it.each(["sort=", "sort=unknown", "sort=added_at_desc&sort=title"])("拒绝非法或重复排序：%s", (value) => {
    expect(respondToGroupList(groups, query(value))).toMatchObject({ status: 400, body: { error: { code: "invalid_sort" } } });
  });

  it("recent 与时间排序顺序相同，最多八项，并可返回真正的空数组", () => {
    const nine = albumGroups(9);
    const before = structuredClone(nine);
    const recent = decodeRecentlyAdded(respondToRecentlyAdded(nine).body);
    const sorted = decodeReleaseGroupAddedPage(respondToGroupList(nine, query("sort=added_at_desc")).body);
    expect(recent.items).toHaveLength(8);
    // 独立期望防止两个接口共用的排序器同时出错而互相比对通过。
    expect(recent.items).toEqual([...nine].reverse().slice(0, 8));
    expect(recent.items).toEqual(sorted.items.slice(0, 8));
    expect(nine).toEqual(before);
    expect(decodeRecentlyAdded(respondToRecentlyAdded([]).body)).toEqual({ items: [] });
  });
});
