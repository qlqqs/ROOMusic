import { describe, expect, it } from "vitest";
import { sampleBody } from "../../test/api-samples";
import { decodeOperationPage, type OperationItemDTO } from "./api/decoders";
import { changeLines, notRevertibleReason, operationTypeLabel } from "./change-summary";
import { parseOperationsSearch } from "./operations-search";
import { governanceKeys } from "./query-keys";

const records = decodeOperationPage(sampleBody("operations/artists")).items;
function recordOf(type: OperationItemDTO["type"]) {
  const item = records.find((record) => record.type === type);
  if (item === undefined) throw new Error(`缺少 ${type} 样本`);
  return item;
}
const upperId = "A0000000-0000-4000-8000-0000000000AB";
const id = upperId.toLowerCase();

describe("艺人操作记录摘要", () => {
  it.each([
    ["artist_merge", "合并艺人", "需要时可以拆开"],
    ["artist_split", "拆开艺人", "需要时可以再合并"],
    ["artist_rename", "修改艺人主名", "需要时可以再改名"],
    ["artist_candidate_ignore", "忽略重复艺人提示", "不能撤销"],
  ] as const)("%s 明确自己的类型与反向办法，不假称专辑不可见或发生后续修改", (type, label, reason) => {
    // 真实 operations/artists 样本没有忽略记录；按公开 DTO 显式登记该类型，不声称后端样本覆盖。
    const item: OperationItemDTO = type === "artist_candidate_ignore"
      ? { ...recordOf("artist_merge"), type, changes: { kind: "artist_candidate_ignore", other_artist: { id, name: "另一位" }, ignored: true } }
      : recordOf(type);
    expect(operationTypeLabel(item)).toBe(label);
    expect(item.revertible).toBe(false);
    expect(notRevertibleReason(item)).toBe(reason);
    expect(notRevertibleReason({ ...item, release: null, album: null })).toBe(reason);
  });

  it("合并摘要使用当时快照的双方名字和完整范围，不被当前主名覆盖", () => {
    const item = recordOf("artist_merge");
    const changes: OperationItemDTO["changes"] = { kind: "artist_merge", source: { id, name: "曾用名" }, target: { id: upperId.toLowerCase(), name: "当时主名" }, name_count: 2, release_count: 3, track_count: 5 };
    expect(changeLines({ ...item, changes })).toEqual(["“曾用名”并入“当时主名”", "涉及 2 个名字、3 个版本、5 首曲目"]);
    expect(changeLines({ changes: { ...changes, name_count: 0, release_count: 0, track_count: 0 } })).toEqual(["“曾用名”并入“当时主名”"]);
  });

  it.each(["aliases", "albums"] as const)("拆开 %s 说明不同范围语义", (mode) => {
    expect(changeLines({ changes: { kind: "artist_split", source: { id, name: "甲" }, target: { id, name: "乙" }, mode, name_count: 1, release_count: 2, track_count: 3 } })).toEqual([
      mode === "aliases" ? "从“甲”把别名拆成“乙”" : "从“甲”按专辑把署名分给“乙”",
      "涉及 1 个名字、2 个版本、3 首曲目",
    ]);
  });

  it("I7：艺人操作不用书名号，专辑归组的作品名仍用书名号", () => {
    expect(changeLines({ changes: { kind: "artist_merge", source: { id, name: "曾用名" }, target: { id, name: "当时主名" }, name_count: 0, release_count: 0, track_count: 0 } })).toEqual(["“曾用名”并入“当时主名”"]);
    expect(changeLines({ changes: { kind: "release_group_change", from: { group_id: id, group_title: "旧作品" }, to: { group_id: id, group_title: "新作品" } } })).toEqual(["从《旧作品》移到《新作品》"]);
  });

  it("改回自动显示和再次改主名有不同说明", () => {
    const from = { name: "人工主名", source: "manual" as const };
    expect(changeLines({ changes: { kind: "artist_rename", from, to: { name: "最常见写法", source: "auto" } } })).toEqual(["主名：人工主名 → 最常见写法（按最常见的写法）"]);
    expect(changeLines({ changes: { kind: "artist_rename", from, to: { name: "新主名", source: "manual" } } })).toEqual(["主名：人工主名 → 新主名"]);
  });

  it("恢复提示是独立类型文案，不称撤销；损坏摘要仍保留艺人类型", () => {
    const changes: OperationItemDTO["changes"] = { kind: "artist_candidate_ignore", other_artist: { id, name: "另一位" }, ignored: false };
    expect(operationTypeLabel({ type: "artist_candidate_ignore", changes })).toBe("恢复重复艺人提示");
    expect(changeLines({ changes })).toEqual(["恢复与“另一位”重复的提示"]);
    expect(operationTypeLabel({ type: "artist_merge", changes: null })).toBe("合并艺人");
    expect(changeLines({ changes: null })).toEqual(["无法显示改动内容"]);
  });
});

describe("操作记录艺人筛选", () => {
  it("艺人 UUID 归一化并与发行、专辑筛选同时保留，供服务端取并集", () => {
    expect(parseOperationsSearch({ artist: upperId, release: upperId, group: upperId, page: "3" })).toEqual({ artist: id, release: id, group: id, page: 3 });
  });

  it.each([undefined, null, "", "not-an-id", 3, [upperId]])("非法 artist %j 显式置为 undefined，不污染合法其他筛选", (artist) => {
    const parsed = parseOperationsSearch({ artist, release: upperId });
    expect(parsed.artist).toBeUndefined();
    expect(Object.hasOwn(parsed, "artist")).toBe(true);
    expect(parsed.release).toBe(id);
  });

  it("艺人筛选和会话代次进入操作记录查询键，切换不复用另一位记录", () => {
    const filter = { releaseId: null, groupId: null, artistId: id, page: 1, pageSize: 50 };
    expect(governanceKeys.operationPage(1, filter)).not.toEqual(governanceKeys.operationPage(1, { ...filter, artistId: null }));
    expect(governanceKeys.operationPage(1, filter)).not.toEqual(governanceKeys.operationPage(2, filter));
  });
});
