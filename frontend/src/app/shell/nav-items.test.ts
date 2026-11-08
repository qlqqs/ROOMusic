import { describe, expect, it } from "vitest";
import { visibleNavGroups } from "./nav-items";

const listening = { label: "聆听", items: ["概览", "专辑", "艺人"] };
const myMusic = { label: "我的音乐", items: ["我喜欢的音乐", "我的收藏", "播放历史", "我的歌单"] };

describe("聆听、我的音乐与资料库导航", () => {
  it("普通成员看到聆听与我的音乐两组", () => {
    expect(visibleNavGroups("user").map((group) => ({ label: group.label, items: group.items.map((item) => item.label) }))).toEqual([listening, myMusic]);
  });
  it("管理员额外看到资料库待办，不再显示配置入口或系统分组", () => {
    expect(visibleNavGroups("admin").map((group) => ({ label: group.label, items: group.items.map((item) => item.label) }))).toEqual([listening, myMusic, { label: "资料库", items: ["元数据", "待认领", "重复艺人"] }]);
  });
  it.each(["user", "admin"] as const)("艺人能力不可用时 %s 不显示艺人和候选入口", (role) => {
    expect(visibleNavGroups(role, { artists: false }).map((group) => ({ label: group.label, items: group.items.map((item) => item.label) }))).toEqual([
      { label: "聆听", items: ["概览", "专辑"] },
      myMusic,
      ...(role === "admin" ? [{ label: "资料库", items: ["元数据", "待认领"] }] : []),
    ]);
  });
});
