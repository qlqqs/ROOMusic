import { describe, expect, it } from "vitest";
import { visibleNavGroups } from "./nav-items";

describe("聆听与资料库导航", () => {
  it("普通成员只看到聆听的三个真实入口", () => {
    expect(visibleNavGroups("user").map((group) => ({ label: group.label, items: group.items.map((item) => item.label) }))).toEqual([{ label: "聆听", items: ["概览", "专辑", "正在播放"] }]);
  });
  it("管理员额外看到资料库分组", () => {
    expect(visibleNavGroups("admin").map((group) => ({ label: group.label, items: group.items.map((item) => item.label) }))).toEqual([{ label: "聆听", items: ["概览", "专辑", "正在播放"] }, { label: "资料库", items: ["扫描目录", "元数据问题", "待认领", "成员", "操作记录"] }]);
  });
});
