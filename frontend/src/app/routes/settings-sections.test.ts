import { describe, expect, it } from "vitest";
import { visibleSettingsSections } from "./settings-sections";

describe("设置分栏", () => {
  it("管理员看到固定顺序的七栏与对应地址", () => {
    expect(visibleSettingsSections("admin").map(({ label, to }) => ({ label, to }))).toEqual([
      { label: "账号", to: "/settings/account" },
      { label: "播放", to: "/settings/playback" },
      { label: "Agent", to: "/settings/agent" },
      { label: "扫描目录", to: "/settings/library" },
      { label: "成员", to: "/settings/members" },
      { label: "操作记录", to: "/settings/operations" },
      { label: "备份", to: "/settings/backups" },
    ]);
  });

  it("成员看到账号、播放与 Agent 栏，重复读取不会改变管理员分栏", () => {
    expect(visibleSettingsSections("user").map(({ label, to }) => ({ label, to }))).toEqual([
      { label: "账号", to: "/settings/account" },
      { label: "播放", to: "/settings/playback" },
      { label: "Agent", to: "/settings/agent" },
    ]);
    expect(visibleSettingsSections("admin")).toHaveLength(7);
  });
});
