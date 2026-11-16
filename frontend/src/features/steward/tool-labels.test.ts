import { Wrench } from "lucide-react";
import { describe, expect, it } from "vitest";
import { toolArgumentOf, toolCountOf, toolDataScopeOf, toolFailureOf, toolLabelOf, toolResultOf, toolWritesOf } from "./tool-labels";

// 后端登记的 16 个只读工具（合同第 3 节）与 C5 的播放、播放历史、收藏与歌单工具。
const tools = [
  "search_library", "list_albums", "get_album", "get_release", "get_track", "list_artists", "get_artist", "list_artist_albums",
  "list_artist_tracks", "get_release_evidence", "list_release_issues", "get_release_issues", "list_scans", "get_scan_report",
  "get_scan_status", "list_operations",
  "get_playback", "get_queue", "control_playback", "play_tracks", "edit_queue", "set_play_mode", "transfer_playback", "set_device_volume",
  "list_play_history", "list_recent_releases", "delete_play_history_item",
  "list_favorites", "list_playlists", "get_playlist", "set_favorite", "create_playlist", "rename_playlist", "delete_playlist",
  "add_playlist_tracks", "remove_playlist_track", "move_playlist_track",
];

describe("工具的中文名称", () => {
  it("登记的工具都有中文名与专门的图标", () => {
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
    expect(toolArgumentOf("search_library", { q: "Furtwängler", type: "artists" }, { title: "别的" })).toBe("Furtwängler");
  });

  it.each([
    [{ title: "Beethoven Symphony No.9" }, "Beethoven Symphony No.9"],
    [{ name: "威廉·富特文格勒" }, "威廉·富特文格勒"],
    [{ representative: { title: "Bayreuth 1951" } }, "Bayreuth 1951"],
    [{ artist: { name: "卡拉扬" } }, "卡拉扬"],
    [{ playlist: { name: "睡前" } }, "睡前"],
  ])("没有搜索词时取结果里对象的标题 %j", (output, expected) => {
    expect(toolArgumentOf("get_album", { id: "x" }, output)).toBe(expected);
  });

  it("都没有（或结果被截断成字符串）时不显示", () => {
    expect(toolArgumentOf("get_album", { id: "x" }, { items: [] })).toBeNull();
    expect(toolArgumentOf("get_album", { id: "x" }, "{\"title\":\"被截断")).toBeNull();
    expect(toolArgumentOf("search_library", { q: "  " }, null)).toBeNull();
  });

  it.each([
    ["control_playback", { action: "pause", zone_id: "z" }, "暂停"],
    ["control_playback", { action: "next" }, "下一首"],
    ["play_tracks", { placement: "end", release_id: "r" }, "加到队尾"],
    ["play_tracks", { placement: "next", track_ids: ["a"] }, "下一首播放"],
    ["edit_queue", { action: "clear" }, "清空"],
    ["set_play_mode", { mode: "repeat_one" }, "单曲循环"],
    ["set_device_volume", { volume: 0.4, muted: false }, "音量 40%"],
    ["set_device_volume", { muted: true }, "静音"],
    ["list_favorites", { kind: "album" }, "专辑"],
    ["set_favorite", { kind: "track", target_id: "t", favorited: true }, "曲目"],
    ["create_playlist", { name: "睡前" }, "睡前"],
    ["rename_playlist", { playlist_id: "p", name: "新名字" }, "新名字"],
  ])("%s 的主要参数取自输入 %j", (name, input, expected) => {
    expect(toolArgumentOf(name, input, { title: "别的" })).toBe(expected);
  });

  it("工具自己的规则取不到时回到通用规则", () => {
    expect(toolArgumentOf("control_playback", { action: "unknown" }, { title: "Presto" })).toBe("Presto");
    expect(toolArgumentOf("delete_playlist", { playlist_id: "p" }, { name: "旧歌单" })).toBe("旧歌单");
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

describe("结果说明", () => {
  it.each([
    ["set_favorite", { favorited: true }, {}, "已收藏"],
    ["set_favorite", { favorited: false }, {}, "已取消收藏"],
    ["set_favorite", {}, { favorited: true }, "已收藏"],
    ["play_tracks", { track_ids: ["a", "b", "c"] }, {}, "3 首"],
    ["add_playlist_tracks", { playlist_id: "p", track_ids: ["a", "b"] }, {}, "2 首"],
    ["create_playlist", { name: "x" }, {}, "已新建"],
    ["delete_play_history_item", { track_id: "t" }, {}, "已删除"],
    ["list_play_history", {}, { pagination: { total: 42 } }, "42 首"],
    ["list_favorites", { kind: "artist" }, { items: [1, 2] }, "2 位"],
    ["list_playlists", {}, { pagination: { total: 3 } }, "3 个"],
  ])("%s %j %j → %s", (name, input, output, expected) => {
    expect(toolResultOf(name, input, output)).toBe(expected);
  });

  it("播放控制没有结果说明", () => {
    expect(toolResultOf("control_playback", { action: "pause" }, {})).toBeNull();
  });
});

describe("工具改动的个人数据", () => {
  it.each([
    ["set_favorite", "favorites"],
    ["create_playlist", "playlists"],
    ["rename_playlist", "playlists"],
    ["delete_playlist", "playlists"],
    ["add_playlist_tracks", "playlists"],
    ["remove_playlist_track", "playlists"],
    ["move_playlist_track", "playlists"],
    ["delete_play_history_item", "history"],
  ] as const)("%s → %s", (name, scope) => {
    expect(toolDataScopeOf(name)).toBe(scope);
  });

  it("只读与播放类工具不刷新", () => {
    for (const name of ["list_favorites", "get_playlist", "list_play_history", "control_playback", "play_tracks", "edit_queue", "list_albums", "future_tool"]) {
      expect(toolDataScopeOf(name)).toBeNull();
    }
  });
});

describe("只读与改动", () => {
  it("播放控制、收藏、歌单与历史的写入是改动类；只读工具与未登记的工具不是", () => {
    for (const name of ["control_playback", "play_tracks", "edit_queue", "set_play_mode", "transfer_playback", "set_device_volume", "set_favorite", "create_playlist", "move_playlist_track", "delete_play_history_item"]) {
      expect(toolWritesOf(name)).toBe(true);
    }
    for (const name of ["get_playback", "get_queue", "list_play_history", "list_favorites", "get_playlist", "list_albums", "future_tool"]) {
      expect(toolWritesOf(name)).toBe(false);
    }
  });
});

describe("失败原因", () => {
  it.each([
    ["not_found", "没找到"],
    ["permission_denied", "没有权限"],
    ["cancelled", "已停止"],
    ["interrupted", "没有完成"],
    ["token_limit", "没有执行"],
    ["zone_offline", "分区不在线"],
    ["renderer_unavailable", "没有可用的输出"],
    ["revision_conflict", "内容已变化"],
    ["queue_full", "队列已满"],
    ["volume_fixed", "这个设备的音量不能调"],
    ["playlist_limit_reached", "歌单数量已到上限"],
    ["playlist_track_limit_reached", "歌单曲目已到上限"],
    ["no_playable_tracks", "没有可播放的曲目"],
    ["tool_execution_failed", "失败"],
    [null, "失败"],
  ] as const)("%s → %s", (code, text) => {
    expect(toolFailureOf(code)).toBe(text);
  });
});
