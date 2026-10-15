import { describe, expect, it } from "vitest";
import { sampleBody } from "../../test/api-samples";
import { decodeOperationPage, type OperationItemDTO } from "./api/decoders";
import { changeLines, defaultVersionText, notRevertibleReason, operationTypeLabel, revertLines } from "./change-summary";

const items = decodeOperationPage(sampleBody("operations/list")).items;
const itemOf = (type: OperationItemDTO["type"], index = 0): OperationItemDTO => {
  const found = items.filter((item) => item.type === type)[index];
  if (found === undefined) throw new Error(`样本里没有 ${type}`);
  return found;
};

describe("operationTypeLabel", () => {
  it("按记录的类型命名", () => {
    expect(operationTypeLabel(itemOf("release_correction"))).toBe("修改发行信息");
    expect(operationTypeLabel(itemOf("release_group_change", 0))).toBe("调整归组");
  });

  it("撤销记录写明撤销的是哪一类；改动读不出来时只写“撤销”", () => {
    expect(operationTypeLabel(itemOf("revert"))).toBe("撤销归组调整");
    const correction = itemOf("release_correction");
    expect(operationTypeLabel({ type: "revert", changes: correction.changes })).toBe("撤销修改");
    expect(operationTypeLabel({ type: "revert", changes: null })).toBe("撤销");
  });
});

describe("changeLines", () => {
  it("字段修正：每个字段一行，写改动前后的有效值；留空与恢复扫描值另加说明", () => {
    expect(changeLines(itemOf("release_correction"))).toEqual([
      "标题：Sample Symphonies → Sample Symphonies (corrected)",
      "年份：1998 → （空）（留空）",
      "厂牌：Sample Label → Corrected Label",
    ]);
    const restored: Pick<OperationItemDTO, "changes"> = {
      changes: { kind: "release_correction", fields: [{ field: "genre", before: { correction: { mode: "set", value: "Jazz" }, effective: "Jazz" }, after: { correction: null, effective: "Classical" } }] },
    };
    expect(changeLines(restored)).toEqual(["流派：Jazz → Classical（恢复为扫描值）"]);
  });

  it("归组：写专辑的移动（标题是当时的专辑名）；撤销记录的前后状态相反", () => {
    const joined = itemOf("release_group_change", 0);
    const lines = changeLines(joined);
    expect(lines).toHaveLength(1);
    expect(lines[0]).toMatch(/^从《.+》移到《.+》$/);
    const revert = itemOf("revert");
    expect(changeLines(revert)).toHaveLength(1);
  });

  it("改动内容不可读时只说明这一点", () => {
    expect(changeLines({ changes: null })).toEqual(["无法显示改动内容"]);
  });
});

describe("revertLines", () => {
  it("字段修正：前后互换，写出撤销之后的样子；改回扫描值与留空另加说明", () => {
    expect(revertLines(itemOf("release_correction"))).toEqual([
      "标题：Sample Symphonies (corrected) → Sample Symphonies（恢复为扫描值）",
      "年份：（空） → 1998（恢复为扫描值）",
      "厂牌：Corrected Label → Sample Label（恢复为扫描值）",
    ]);
    const cleared: Pick<OperationItemDTO, "changes"> = {
      changes: { kind: "release_correction", fields: [{ field: "genre", before: { correction: { mode: "null" }, effective: null }, after: { correction: { mode: "set", value: "Jazz" }, effective: "Jazz" } }] },
    };
    expect(revertLines(cleared)).toEqual(["流派：Jazz → （空）（留空）"]);
    const edited: Pick<OperationItemDTO, "changes"> = {
      changes: { kind: "release_correction", fields: [{ field: "genre", before: { correction: { mode: "set", value: "Jazz" }, effective: "Jazz" }, after: { correction: { mode: "set", value: "Opera" }, effective: "Opera" } }] },
    };
    expect(revertLines(edited)).toEqual(["流派：Opera → Jazz"]);
  });

  it("归组：从现在的专辑移回原来的专辑", () => {
    const joined = itemOf("release_group_change", 1);
    const [line] = revertLines(joined);
    expect(line).toMatch(/^从《.+》移回《.+》$/);
    expect(changeLines(joined)[0]).toMatch(/^从《.+》移到《.+》$/);
  });

  it("改动内容不可读时只说明这一点", () => {
    expect(revertLines({ changes: null })).toEqual(["无法显示改动内容"]);
  });
});

describe("notRevertibleReason", () => {
  it("可以撤销时没有原因", () => {
    expect(notRevertibleReason(itemOf("release_correction"))).toBeNull();
  });

  it("撤销记录、已撤销、专辑已不可见与之后又有修改各有原因", () => {
    const [revert, detach, join] = [itemOf("revert"), itemOf("release_group_change", 0), itemOf("release_group_change", 1)];
    expect(notRevertibleReason(revert)).toBe("撤销不能再撤销");
    expect(notRevertibleReason(detach)).toBe("已撤销");
    expect(notRevertibleReason(join)).toBe("之后又有修改");
    expect(notRevertibleReason({ ...join, release: null })).toBe("专辑已不可见");
    expect(notRevertibleReason({ ...join, release: join.release === null ? null : { ...join.release, visible: false } })).toBe("专辑已不可见");
  });
});

// 专辑目标的默认版本记录（album 有值、release 为 null）：样本里是“自动选择 → 双碟专辑”的一次设定。
const defaultRecord = (() => {
  const found = decodeOperationPage(sampleBody("operations/list-by-group")).items[0];
  if (found === undefined) throw new Error("样本里没有默认版本记录");
  return found;
})();

describe("默认版本记录", () => {
  it("类型名；撤销记录写“撤销默认版本调整”", () => {
    expect(operationTypeLabel(defaultRecord)).toBe("设定默认版本");
    expect(operationTypeLabel({ type: "revert", changes: defaultRecord.changes })).toBe("撤销默认版本调整");
  });

  it("改动一行：默认版本前后，版本写标题与（年份 · 版本说明）；撤销预览前后互换", () => {
    expect(changeLines(defaultRecord)).toEqual(["默认版本：自动选择 → 《Sample Symphonies (corrected)》（Remastered）"]);
    expect(revertLines(defaultRecord)).toEqual(["默认版本：《Sample Symphonies (corrected)》（Remastered） → 自动选择"]);
  });

  it("版本的显示：年份与版本说明有才写，都没有只写标题", () => {
    const version = { release_id: "00000000-0000-4000-8000-000000000001", title: "Sample", year: 2015, edition: "Remaster" };
    expect(defaultVersionText(version)).toBe("《Sample》（2015 · Remaster）");
    expect(defaultVersionText({ ...version, edition: null })).toBe("《Sample》（2015）");
    expect(defaultVersionText({ ...version, year: null })).toBe("《Sample》（Remaster）");
    expect(defaultVersionText({ ...version, year: null, edition: null })).toBe("《Sample》");
    expect(defaultVersionText(null)).toBe("自动选择");
  });

  it("专辑目标的记录按专辑判断能不能撤销：专辑不可见与之后又有调整各有原因", () => {
    expect(notRevertibleReason(defaultRecord)).toBeNull();
    expect(notRevertibleReason({ ...defaultRecord, revertible: false })).toBe("之后又有修改");
    expect(notRevertibleReason({ ...defaultRecord, revertible: false, album: defaultRecord.album === null ? null : { ...defaultRecord.album, visible: false } })).toBe("专辑已不可见");
    expect(notRevertibleReason({ ...defaultRecord, revertible: false, album: null })).toBe("专辑已不可见");
    expect(notRevertibleReason({ ...defaultRecord, revertible: false, reverted_by: "00000000-0000-4000-8000-000000000009" })).toBe("已撤销");
  });
});
