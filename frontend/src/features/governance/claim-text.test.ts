import { describe, expect, it } from "vitest";
import { correctionNames, holdsText, locationText, reviewNames, suggestionReasonText } from "./claim-text";

const none: Parameters<typeof holdsText>[0] = { corrections: [], album: false, default: false, reviews: [] };

describe("holdsText", () => {
  it("按固定的顺序写出留着的修改，没有的类别不写", () => {
    expect(holdsText({ corrections: ["title", "genre", "label"], album: true, default: true, reviews: ["grouping"] })).toBe("修正 3 项 · 专辑 · 默认版本 · 核对 1 项");
    expect(holdsText({ ...none, corrections: ["year"] })).toBe("修正 1 项");
    expect(holdsText({ ...none, album: true })).toBe("专辑");
    expect(holdsText({ ...none, default: true, reviews: ["source_type", "media_type"] })).toBe("默认版本 · 核对 2 项");
    expect(holdsText(none)).toBe("");
  });
});

describe("locationText", () => {
  it("扫描目录加相对目录；相对目录为空时只写扫描目录", () => {
    expect(locationText({ root_name: "classical", directory: "Bach/Suites" })).toBe("classical / Bach/Suites");
    expect(locationText({ root_name: "classical", directory: "" })).toBe("classical");
  });
});

describe("字段与问题的名字", () => {
  it("用中文名并用顿号连接", () => {
    expect(correctionNames(["title", "album_artist", "barcode"])).toBe("标题、专辑艺人、条码");
    expect(reviewNames(["source_type", "grouping"])).toBe("来源类型、归组冲突");
    expect(correctionNames([])).toBe("");
  });
});

describe("suggestionReasonText", () => {
  it("同样的文件写命中的文件数，同一目录就写同一目录，两者都有时用顿号连接", () => {
    expect(suggestionReasonText({ reasons: ["same_files"], same_files: 12 })).toBe("同样的文件（12 个）");
    expect(suggestionReasonText({ reasons: ["same_directory"], same_files: 0 })).toBe("同一目录");
    expect(suggestionReasonText({ reasons: ["same_files", "same_directory"], same_files: 3 })).toBe("同样的文件（3 个）、同一目录");
  });

  it("后端新增的理由码原样显示，不让页面失败", () => {
    expect(suggestionReasonText({ reasons: ["same_fingerprint"], same_files: 0 })).toBe("same_fingerprint");
    expect(suggestionReasonText({ reasons: [], same_files: 0 })).toBe("");
  });
});
