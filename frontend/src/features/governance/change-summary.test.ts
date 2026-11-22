import { describe, expect, it } from "vitest";
import { sampleBody } from "../../test/api-samples";
import { decodeOperationPage, type OperationItemDTO } from "./api/decoders";
import { changeLines, defaultVersionText, notRevertibleReason, operationOriginLabel, operationTypeLabel, revertLines } from "./change-summary";

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
      "流派：Classical · Baroque → Romantic · Classical",
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
      "流派：Romantic · Classical → Classical · Baroque（恢复为扫描值）",
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
    expect(notRevertibleReason(revert)).toBe("已完成");
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

// 核对问题的记录：样本里依次是核对归组冲突、核对缺失曲目（1 首）、取消核对归组冲突与撤销这次取消。
const issueRecords = decodeOperationPage(sampleBody("operations/list-issue-reviews")).items;
const issueRecord = (index: number): OperationItemDTO => {
  const found = issueRecords[index];
  if (found === undefined) throw new Error(`样本里没有第 ${index} 条核对记录`);
  return found;
};

describe("核对问题记录", () => {
  it("类型名；撤销记录写“撤销核对”", () => {
    expect(operationTypeLabel(issueRecord(1))).toBe("核对问题");
    expect(operationTypeLabel(issueRecord(0))).toBe("撤销核对");
  });

  it("改动一行：问题与“标记为已核对”或“取消核对”，缺失曲目带曲目数；撤销记录写恢复成了什么", () => {
    expect(changeLines(issueRecord(3))).toEqual(["归组冲突：标记为已核对"]);
    expect(changeLines(issueRecord(2))).toEqual(["缺失曲目（1 首）：标记为已核对"]);
    expect(changeLines(issueRecord(1))).toEqual(["归组冲突：取消核对"]);
    expect(changeLines(issueRecord(0))).toEqual(["归组冲突：标记为已核对"]);
  });

  it("撤销预览前后互换：撤销核对是取消核对，撤销取消核对是重新标记", () => {
    expect(revertLines(issueRecord(2))).toEqual(["缺失曲目（1 首）：取消核对"]);
    expect(revertLines(issueRecord(1))).toEqual(["归组冲突：标记为已核对"]);
  });

  it("来源类型与介质类型用字段的中文名", () => {
    const changes = { kind: "release_issue_review" as const, issue: "media_type" as const, from: { reviewed: false, track_count: null }, to: { reviewed: true, track_count: null } };
    expect(changeLines({ changes })).toEqual(["介质类型：标记为已核对"]);
    expect(changeLines({ changes: { ...changes, issue: "source_type" } })).toEqual(["来源类型：标记为已核对"]);
  });

  it("“之后又有修改”与“已完成”的原因与其他记录相同", () => {
    expect(notRevertibleReason(issueRecord(2))).toBe("之后又有修改");
    expect(notRevertibleReason(issueRecord(0))).toBe("已完成");
  });
});

describe("操作的来源（M6 C6）", () => {
  it("确认方案后执行、直接执行、MCP；没有来源时不显示", () => {
    expect(operationOriginLabel({ source: "chat", plan_id: "00000000-0000-4000-8000-0000000000c1", mode: "assistant" })).toBe("管家（确认）");
    expect(operationOriginLabel({ source: "chat", plan_id: null, mode: "operator" })).toBe("管家（直接执行）");
    expect(operationOriginLabel({ source: "chat", mode: "assistant" })).toBe("管家（直接执行）");
    expect(operationOriginLabel({ source: "mcp" })).toBe("MCP");
    expect(operationOriginLabel(null)).toBeNull();
    expect(operationOriginLabel(undefined)).toBeNull();
  });
});

describe("自动整理的记录（M7 A）", () => {
  const OP = "00000000-0000-4000-8000-0000000000b1";
  const written: Pick<OperationItemDTO, "type" | "changes"> = {
    type: "release_auto_correction",
    changes: {
      kind: "release_auto_correction",
      fields: [
        { field: "year", before: { auto: null, effective: null }, after: { auto: { mode: "set", value: 1998, operation_id: OP }, effective: 1998 } },
        { field: "label", before: { auto: { mode: "set", value: "Old Label", operation_id: OP }, effective: "Old Label" }, after: { auto: { mode: "null", operation_id: OP }, effective: null } },
        // 标题有人工修正：自动值写进去了，但显示的值没有变。
        { field: "title", before: { auto: null, effective: "Manual Title" }, after: { auto: { mode: "set", value: "Source Title", operation_id: OP }, effective: "Manual Title" } },
      ],
    },
  };

  it("类型名；撤销记录写“撤销自动补全”", () => {
    expect(operationTypeLabel(written)).toBe("自动补全发行信息");
    expect(operationTypeLabel({ type: "revert", changes: written.changes })).toBe("撤销自动补全");
  });

  it("改动一行：写有效值的前后；留空另加说明；有人工修正时写自动值本身的变化", () => {
    expect(changeLines(written)).toEqual([
      "年份：（空） → 1998",
      "厂牌：Old Label → （空）（留空）",
      "标题：自动值 无 → Source Title（人工修正优先，显示不变）",
    ]);
  });

  it("撤销预览前后互换：没有更早的自动值时写“取消自动补全”", () => {
    expect(revertLines(written)).toEqual([
      "年份：1998 → （空）（取消自动补全）",
      "厂牌：（空） → Old Label",
      "标题：自动值 Source Title → 无（人工修正优先，显示不变）",
    ]);
  });

  it("流派列表逐项比较：内容相同的两个列表算没有变化", () => {
    const genres: Pick<OperationItemDTO, "changes"> = {
      changes: {
        kind: "release_auto_correction",
        fields: [{ field: "genre", before: { auto: null, effective: ["Jazz", "Blues"] }, after: { auto: { mode: "set", value: ["Rock"], operation_id: OP }, effective: ["Jazz", "Blues"] } }],
      },
    };
    expect(changeLines(genres)).toEqual(["流派：自动值 无 → Rock（人工修正优先，显示不变）"]);
  });

  it("来源标签带来源标识；标识缺失时只写“自动整理”", () => {
    expect(operationOriginLabel({ source: "curation", provider_kind: "source", provider: "musicbrainz", basis: "条码一致" })).toBe("自动整理 · MusicBrainz");
    expect(operationOriginLabel({ source: "curation" })).toBe("自动整理");
  });

  it("撤不了的原因：已撤销，或同一个字段被更晚的自动整理改写；不看专辑是否可见", () => {
    const hidden = { id: "00000000-0000-4000-8000-0000000000a1", title: "Hidden", group_id: "00000000-0000-4000-8000-0000000000a2", visible: false };
    expect(notRevertibleReason({ type: "release_auto_correction", revertible: false, reverted_by: null, release: hidden, album: null })).toBe("之后又被自动整理改写");
    expect(notRevertibleReason({ type: "release_auto_correction", revertible: false, reverted_by: "00000000-0000-4000-8000-0000000000a3", release: hidden, album: null })).toBe("已撤销");
    expect(notRevertibleReason({ type: "release_auto_correction", revertible: true, reverted_by: null, release: hidden, album: null })).toBeNull();
  });
});

describe("自动补的封面与歌词（M7 B2）", () => {
  const artwork: Pick<OperationItemDTO, "type" | "changes"> = {
    type: "release_external_artwork",
    changes: { kind: "release_external_artwork", source: "coverartarchive", external_id: "release-1/829521842", artwork: { resource_id: "a.jpeg", mime: "image/jpeg", width: 1200, height: 1188 } },
  };
  const lyrics: Pick<OperationItemDTO, "type" | "changes"> = {
    type: "track_external_lyrics",
    changes: { kind: "track_external_lyrics", source: "lrclib", external_id: "3396226", track: { id: "00000000-0000-4000-8000-0000000000c1", title: "Sample Song" }, lyrics_kind: "synced", lines: 42 },
  };
  const plain: Pick<OperationItemDTO, "changes"> = {
    changes: { kind: "track_external_lyrics", source: "other-source", external_id: "7", track: { id: "00000000-0000-4000-8000-0000000000c2", title: "Other" }, lyrics_kind: "plain", lines: 3 },
  };

  it("类型名与撤销记录的类型名", () => {
    expect(operationTypeLabel(artwork)).toBe("自动补封面");
    expect(operationTypeLabel(lyrics)).toBe("自动补歌词");
    expect(operationTypeLabel({ type: "revert", changes: artwork.changes })).toBe("撤销自动补的封面");
    expect(operationTypeLabel({ type: "revert", changes: lyrics.changes })).toBe("撤销自动补的歌词");
  });

  it("改动一行：封面写尺寸与来源，歌词写曲目、形态、行数与来源；不认识的源写标识本身", () => {
    expect(changeLines(artwork)).toEqual(["补了 1200×1188 的封面，来自 Cover Art Archive"]);
    expect(changeLines(lyrics)).toEqual(["给《Sample Song》补了同步歌词（42 行），来自 LRCLIB"]);
    expect(changeLines(plain)).toEqual(["给《Other》补了歌词（3 行），来自 other-source"]);
  });

  it("撤销前的说明：去掉自动补的那一份", () => {
    expect(revertLines(artwork)).toEqual(["去掉自动补的封面（1200×1188 的封面，来自 Cover Art Archive）"]);
    expect(revertLines(lyrics)).toEqual(["去掉给《Sample Song》自动补的同步歌词（42 行），来自 LRCLIB"]);
  });

  it("撤不了的原因：已撤销，或已经不是这一份了；不看专辑是否可见", () => {
    const hidden = { id: "00000000-0000-4000-8000-0000000000a1", title: "Hidden", group_id: "00000000-0000-4000-8000-0000000000a2", visible: false };
    expect(notRevertibleReason({ type: "release_external_artwork", revertible: false, reverted_by: null, release: hidden, album: null })).toBe("已被替换");
    expect(notRevertibleReason({ type: "track_external_lyrics", revertible: false, reverted_by: "00000000-0000-4000-8000-0000000000a3", release: hidden, album: null })).toBe("已撤销");
    expect(notRevertibleReason({ type: "track_external_lyrics", revertible: true, reverted_by: null, release: hidden, album: null })).toBeNull();
  });
});

describe("外部对应（M7 C）", () => {
  const id = (n: number) => `00000000-0000-4000-8000-${String(n).padStart(12, "0")}`;
  const link: Pick<OperationItemDTO, "type" | "changes"> = {
    type: "external_link",
    changes: {
      kind: "external_link",
      source: "musicbrainz",
      method: "barcode",
      confidence: "certain",
      external: { title: "Sample Album", artist: "Sample Artist", date: "2015-03-02", country: "JP" },
      links: [
        { object_type: "release", object_id: id(1), external_id: "11111111-1111-4111-8111-111111111111", external_group_id: "22222222-2222-4222-8222-222222222222" },
        { object_type: "track", object_id: id(2), external_id: "33333333-3333-4333-8333-333333333333" },
        { object_type: "track", object_id: id(3), external_id: "44444444-4444-4444-8444-444444444444" },
        { object_type: "release_group", object_id: id(4), external_id: "22222222-2222-4222-8222-222222222222" },
      ],
    },
  };
  const bare: Pick<OperationItemDTO, "changes"> = {
    changes: {
      kind: "external_link",
      source: "musicbrainz",
      method: "model_judgement",
      confidence: "likely",
      external: { title: "", artist: "", date: "", country: "" },
      links: [{ object_type: "release", object_id: id(1), external_id: "11111111-1111-4111-8111-111111111111" }],
    },
  };

  it("类型名与撤销记录的类型名", () => {
    expect(operationTypeLabel(link)).toBe("对上外部资料");
    expect(operationTypeLabel({ type: "revert", changes: link.changes })).toBe("解除外部对应");
  });

  it("改动：外部发行、依据与一并对上的专辑与曲目；空的部分不写，不认识的依据写标识本身", () => {
    expect(changeLines(link)).toEqual(["对上 MusicBrainz：《Sample Album》— Sample Artist（2015 · JP）", "依据条码，同时对上专辑、2 首曲目"]);
    expect(changeLines(bare)).toEqual(["对上 MusicBrainz：外部发行", "依据model_judgement"]);
  });

  it("撤销前的说明与撤销记录都写成解除", () => {
    const expected = ["解除与 MusicBrainz：《Sample Album》— Sample Artist（2015 · JP） 的对应", "同时去掉专辑、2 首曲目的对应"];
    expect(revertLines(link)).toEqual(expected);
    expect(changeLines({ type: "revert", changes: link.changes })).toEqual(expected);
  });

  it("撤不了的原因：对应已被改动；已撤销时写已撤销", () => {
    const release = { id: id(1), title: "Album", group_id: id(4), visible: false };
    expect(notRevertibleReason({ type: "external_link", revertible: false, reverted_by: null, release, album: null })).toBe("对应已被改动");
    expect(notRevertibleReason({ type: "external_link", revertible: false, reverted_by: id(9), release, album: null })).toBe("已撤销");
  });
});

describe("自动整理的记录：后端样本", () => {
  const [revert, written] = decodeOperationPage(sampleBody("operations/auto-list")).items;
  if (revert === undefined || written === undefined) throw new Error("样本里没有自动整理的记录");

  it("类型名、来源标签与改动", () => {
    expect(operationTypeLabel(written)).toBe("自动补全发行信息");
    expect(operationOriginLabel(written.origin)).toBe("自动整理 · fixture-catalog");
    expect(changeLines(written)).toEqual(["标题：Album A → 合成自动标题", "年份：1998 → （空）（留空）"]);
  });

  it("撤销记录写恢复成了什么；两条都不能再撤销，各有原因", () => {
    expect(operationTypeLabel(revert)).toBe("撤销自动补全");
    expect(operationOriginLabel(revert.origin)).toBeNull();
    expect(changeLines(revert)).toEqual(["标题：合成自动标题 → Album A（取消自动补全）", "年份：（空） → 1998（取消自动补全）"]);
    expect(notRevertibleReason(written)).toBe("已撤销");
    expect(notRevertibleReason(revert)).toBe("已完成");
  });
});

describe("自动补的封面与歌词：后端样本", () => {
  const [lyricsRevert, artworkRevert, lyrics, artwork] = decodeOperationPage(sampleBody("operations/external-list")).items;
  if (lyricsRevert === undefined || artworkRevert === undefined || lyrics === undefined || artwork === undefined) throw new Error("样本里没有自动补的封面与歌词");

  it("类型名、来源标签与改动", () => {
    expect([operationTypeLabel(artwork), operationTypeLabel(lyrics)]).toEqual(["自动补封面", "自动补歌词"]);
    expect([operationOriginLabel(artwork.origin), operationOriginLabel(lyrics.origin)]).toEqual(["自动整理 · Cover Art Archive", "自动整理 · LRCLIB"]);
    expect(changeLines(artwork)).toEqual(["补了 1×1 的封面，来自 Cover Art Archive"]);
    expect(changeLines(lyrics)).toEqual(["给《Album A track 1》补了同步歌词（2 行），来自 LRCLIB"]);
  });

  it("撤销记录带的是被撤销的那一份；四条都不能再撤销，各有原因", () => {
    expect([operationTypeLabel(artworkRevert), operationTypeLabel(lyricsRevert)]).toEqual(["撤销自动补的封面", "撤销自动补的歌词"]);
    expect(artworkRevert.changes).toEqual(artwork.changes);
    expect(lyricsRevert.changes).toEqual(lyrics.changes);
    expect(changeLines(artworkRevert)).toEqual(["去掉自动补的封面（1×1 的封面，来自 Cover Art Archive）"]);
    expect(changeLines(lyricsRevert)).toEqual(["去掉给《Album A track 1》自动补的同步歌词（2 行），来自 LRCLIB"]);
    expect([notRevertibleReason(artwork), notRevertibleReason(lyrics)]).toEqual(["已撤销", "已撤销"]);
    expect([notRevertibleReason(artworkRevert), notRevertibleReason(lyricsRevert)]).toEqual(["已完成", "已完成"]);
  });
});
