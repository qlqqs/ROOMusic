import { describe, expect, it } from "vitest";
import { basisSourceLabel, planFieldLabel, planValueText } from "./plan-text";

describe("方案卡片的文字", () => {
  it.each([
    ["title", "标题"],
    ["catalog_number", "编号"],
    ["default_release_id", "默认版本"],
    ["group", "所在专辑"],
    ["correction.label", "厂牌"],
    ["issue_review.grouping", "归组冲突"],
    ["ignored:a:b", "重复艺人提示"],
    ["future_field", "future_field"],
  ])("字段 %s → %s", (field, label) => {
    expect(planFieldLabel(field)).toBe(label);
  });

  it.each([
    [null, "（空）"],
    ["", "（空）"],
    ["EMI", "EMI"],
    [1951, "1951"],
    [true, "是"],
    [["国语流行乐", "台语流行乐"], "国语流行乐 · 台语流行乐"],
    [{ id: "g", title: "Bayreuth 1951" }, "Bayreuth 1951"],
    [{ name: "富特文格勒", scope: {} }, "富特文格勒"],
  ])("值 %j → %s", (value, text) => {
    expect(planValueText(value)).toBe(text);
  });

  it("依据的来源", () => {
    expect(basisSourceLabel("local")).toBe("本地证据");
    expect(basisSourceLabel("external")).toBe("外部资料");
    expect(basisSourceLabel("mixed")).toBe("本地证据与外部资料");
  });
});
