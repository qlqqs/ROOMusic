import { describe, expect, it } from "vitest";
import { visibleNavGroups } from "./nav-items";

describe("聆听与资料库导航", () => {
  it("普通成员看到聆听的五个真实入口", () => {
    expect(visibleNavGroups("user").map((group) => ({ label: group.label, items: group.items.map((item) => item.label) }))).toEqual([{ label: "聆听", items: ["概览", "专辑", "艺人", "我的音乐", "正在播放"] }]);
  });
  it("管理员额外看到资料库与系统分组", () => {
    expect(visibleNavGroups("admin").map((group) => ({ label: group.label, items: group.items.map((item) => item.label) }))).toEqual([{ label: "聆听", items: ["概览", "专辑", "艺人", "我的音乐", "正在播放"] }, { label: "资料库", items: ["扫描目录", "元数据问题", "待认领", "可能是同一位", "成员", "操作记录"] }, { label: "系统", items: ["备份"] }]);
  });
  it.each(["user", "admin"] as const)("艺人能力不可用时 %s 不显示艺人和候选入口", (role) => {
    expect(visibleNavGroups(role, { artists: false }).map((group) => ({ label: group.label, items: group.items.map((item) => item.label) }))).toEqual([
      { label: "聆听", items: ["概览", "专辑", "我的音乐", "正在播放"] },
      ...(role === "admin" ? [{ label: "资料库", items: ["扫描目录", "元数据问题", "待认领", "成员", "操作记录"] }, { label: "系统", items: ["备份"] }] : []),
    ]);
  });
});
