import { Wrench } from "lucide-react";
import { describe, expect, it } from "vitest";
import { toolArgumentOf, toolCountOf, toolFailureOf, toolLabelOf } from "./tool-labels";

// 后端登记的 16 个只读工具（合同第 3 节）。
const tools = [
  "search_library", "list_albums", "get_album", "get_release", "get_track", "list_artists", "get_artist", "list_artist_albums",
  "list_artist_tracks", "get_release_evidence", "list_release_issues", "get_release_issues", "list_scans", "get_scan_report",
  "get_scan_status", "list_operations",
];

describe("工具的中文名称", () => {
  it("16 个工具都有中文名与专门的图标", () => {
    for (const name of tools) {
      const label = toolLabelOf(name);
      expect(label.label).not.toBe(name);
      expect(label.icon).not.toBe(Wrench);
    }
  });

  it("未登记的工具名原样显示", () => {
    expect(toolLabelOf("future_tool")).toEqual({ label: "future_tool", icon: Wrench });
  });
});

describe("主要参数", () => {
  it("优先取输入里的搜索词", () => {
    expect(toolArgumentOf({ q: "Furtwängler", type: "artists" }, { title: "别的" })).toBe("Furtwängler");
  });

  it.each([
    [{ title: "Beethoven Symphony No.9" }, "Beethoven Symphony No.9"],
    [{ name: "威廉·富特文格勒" }, "威廉·富特文格勒"],
    [{ representative: { title: "Bayreuth 1951" } }, "Bayreuth 1951"],
    [{ artist: { name: "卡拉扬" } }, "卡拉扬"],
  ])("没有搜索词时取结果里对象的标题 %j", (output, expected) => {
    expect(toolArgumentOf({ id: "x" }, output)).toBe(expected);
  });

  it("都没有（或结果被截断成字符串）时不显示", () => {
    expect(toolArgumentOf({ id: "x" }, { items: [] })).toBeNull();
    expect(toolArgumentOf({ id: "x" }, "{\"title\":\"被截断")).toBeNull();
    expect(toolArgumentOf({ q: "  " }, null)).toBeNull();
  });
});

describe("结果数量", () => {
  it("取 pagination.total，没有分页时取 items 的长度", () => {
    expect(toolCountOf("list_albums", {}, { items: [1, 2], pagination: { total: 31 } })).toBe("31 张");
    expect(toolCountOf("list_operations", {}, { items: [1, 2, 3] })).toBe("3 条");
    expect(toolCountOf("list_artists", {}, { pagination: { total: 12_345 } })).toBe("12,345 位");
  });

  it("搜索按类型给单位", () => {
    expect(toolCountOf("search_library", { q: "a", type: "artists" }, { pagination: { total: 2 } })).toBe("2 位");
    expect(toolCountOf("search_library", { q: "a", type: "tracks" }, { pagination: { total: 9 } })).toBe("9 首");
  });

  it("没有单位的工具、没有数量的结果、截断的结果都不显示", () => {
    expect(toolCountOf("get_track", {}, { items: [1] })).toBeNull();
    expect(toolCountOf("list_albums", {}, { title: "x" })).toBeNull();
    expect(toolCountOf("list_albums", {}, "{\"items\":[")).toBeNull();
    expect(toolCountOf("future_tool", {}, { items: [1] })).toBeNull();
  });
});

describe("失败原因", () => {
  it.each([
    ["not_found", "没找到"],
    ["permission_denied", "没有权限"],
    ["cancelled", "已停止"],
    ["interrupted", "没有完成"],
    ["token_limit", "没有执行"],
    ["tool_execution_failed", "失败"],
    [null, "失败"],
  ] as const)("%s → %s", (code, text) => {
    expect(toolFailureOf(code)).toBe(text);
  });
});
