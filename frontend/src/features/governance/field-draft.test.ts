import { describe, expect, it } from "vitest";
import { sampleBody } from "../../test/api-samples";
import { decodeCorrectionState, type CorrectableField, type CorrectionFieldDTO, type CorrectionStateDTO } from "./api/decoders";
import { baselineDraft, confirmScanDraft, draftOf, fieldError, normalizeText, planChanges, type DraftEdits } from "./field-draft";

const initial = decodeCorrectionState(sampleBody("corrections/get"));
const corrected = decodeCorrectionState(sampleBody("corrections/get-corrected"));

function fieldOf(state: CorrectionStateDTO, name: CorrectableField): CorrectionFieldDTO {
  const field = state.fields.find((item) => item.field === name);
  if (field === undefined) throw new Error(`状态里没有字段 ${name}`);
  return field;
}

describe("normalizeText", () => {
  it("去掉首尾空白并折叠内部空白，与后端一致", () => {
    expect(normalizeText("  新   标题\t\n续 ")).toBe("新 标题 续");
    expect(normalizeText("   ")).toBe("");
  });
});

describe("baselineDraft 与 draftOf", () => {
  it("没有修正是“使用扫描值”，文本预填扫描值；扫描值为空时预填空串", () => {
    expect(baselineDraft(fieldOf(initial, "title"))).toEqual({ mode: "scan", text: "Sample Symphonies" });
    expect(baselineDraft(fieldOf(initial, "year"))).toEqual({ mode: "scan", text: "1998" });
    const withoutScan: CorrectionFieldDTO = { field: "genre", scan_value: null, correction: null, effective_value: null, uncertain: false };
    expect(baselineDraft(withoutScan)).toEqual({ mode: "scan", text: "" });
  });

  it("设值是“自定义”，设空是“留空”（保留扫描值作为再次自定义时的预填）", () => {
    expect(baselineDraft(fieldOf(corrected, "title"))).toEqual({ mode: "custom", text: "Sample Symphonies (corrected)" });
    expect(baselineDraft(fieldOf(corrected, "year"))).toEqual({ mode: "empty", text: "1998" });
  });

  it("动过的字段取用户的输入，没动过的取基线", () => {
    const edits: DraftEdits = { title: { mode: "custom", text: "我的标题" } };
    expect(draftOf(fieldOf(initial, "title"), edits)).toEqual({ mode: "custom", text: "我的标题" });
    expect(draftOf(fieldOf(initial, "genre"), edits)).toEqual({ mode: "scan", text: "Classical" });
  });
});

describe("fieldError", () => {
  it("使用扫描值永远没有错误", () => {
    expect(fieldError("title", { mode: "scan", text: "" })).toBeNull();
  });

  it("标题与专辑艺人不能留空，其余字段可以", () => {
    expect(fieldError("title", { mode: "empty", text: "" })).toBe("标题不能留空。");
    expect(fieldError("album_artist", { mode: "empty", text: "" })).toBe("专辑艺人不能留空。");
    for (const field of ["year", "genre", "label", "catalog_number", "edition", "barcode"] as const) {
      expect(fieldError(field, { mode: "empty", text: "" })).toBeNull();
    }
  });

  it("自定义的文本去掉空白后不能为空，并提示用“留空”清空", () => {
    expect(fieldError("genre", { mode: "custom", text: "  \t " })).toBe("请输入内容；要清空这个字段，请选择“留空”。");
    expect(fieldError("title", { mode: "custom", text: "" })).toBe("标题不能为空。");
    expect(fieldError("genre", { mode: "custom", text: " 古典 " })).toBeNull();
  });

  it("年份必须是 1 到 9999 的整数", () => {
    for (const text of ["1", "1975", "9999", " 2001 "]) expect(fieldError("year", { mode: "custom", text })).toBeNull();
    for (const text of ["0", "10000", "19.5", "二〇〇一", "1e3", "-5", "+5", "abc"]) {
      expect(fieldError("year", { mode: "custom", text }), text).toBe("年份必须是 1 到 9999 的整数。");
    }
  });

  it("拒绝控制字符；文本不超过 4096 字节（中文按字节计）", () => {
    expect(fieldError("genre", { mode: "custom", text: "a\u0007b" })).toBe("含有不允许的字符。");
    expect(fieldError("genre", { mode: "custom", text: "x".repeat(4096) })).toBeNull();
    expect(fieldError("genre", { mode: "custom", text: "x".repeat(4097) })).toContain("4096");
    expect(fieldError("genre", { mode: "custom", text: "古".repeat(1365) })).toBeNull(); // 4095 字节
    expect(fieldError("genre", { mode: "custom", text: "古".repeat(1366) })).toContain("4096"); // 4098 字节
  });
});

describe("planChanges", () => {
  it("没动过的字段既不校验也不提交", () => {
    expect(planChanges(initial, {})).toEqual({ changes: {}, fields: [], errors: {} });
  });

  it("设值：使用规范化后的文本；年份是整数；等于扫描值也是有效修正", () => {
    const planned = planChanges(initial, {
      title: { mode: "custom", text: "  新   标题 " },
      year: { mode: "custom", text: " 1975 " },
      genre: { mode: "custom", text: "Classical" },
    });
    expect(planned.changes).toEqual({
      title: { mode: "set", value: "新 标题" },
      year: { mode: "set", value: 1975 },
      genre: { mode: "set", value: "Classical" },
    });
    expect(planned.fields).toEqual(["title", "year", "genre"]);
    expect(planned.errors).toEqual({});
  });

  it("留空与恢复为扫描值", () => {
    expect(planChanges(initial, { year: { mode: "empty", text: "" } }).changes).toEqual({ year: { mode: "null" } });
    // 有修正时选“使用扫描值”是 clear；没有修正时选“使用扫描值”没有变化。
    expect(planChanges(corrected, { title: { mode: "scan", text: "" } }).changes).toEqual({ title: { mode: "clear" } });
    expect(planChanges(initial, { title: { mode: "scan", text: "" } }).changes).toEqual({});
  });

  it("相对基线没有变化的字段不提交（改回原样、重复选择同一个状态）", () => {
    expect(planChanges(corrected, { title: { mode: "custom", text: "Sample Symphonies (corrected)" } }).changes).toEqual({});
    expect(planChanges(corrected, { title: { mode: "custom", text: " Sample   Symphonies (corrected) " } }).changes).toEqual({});
    expect(planChanges(corrected, { year: { mode: "empty", text: "" } }).changes).toEqual({});
    expect(planChanges(corrected, { label: { mode: "custom", text: "Corrected Label" } }).changes).toEqual({});
    expect(planChanges(corrected, { label: { mode: "custom", text: "Another Label" } }).changes).toEqual({ label: { mode: "set", value: "Another Label" } });
  });

  it("有错误的字段只报告错误，不进入提交；其余字段照常提交", () => {
    const planned = planChanges(initial, { title: { mode: "empty", text: "" }, year: { mode: "custom", text: "abc" }, genre: { mode: "empty", text: "" } });
    expect(planned.errors).toEqual({ title: "标题不能留空。", year: "年份必须是 1 到 9999 的整数。" });
    expect(planned.changes).toEqual({ genre: { mode: "null" } });
    expect(planned.fields).toEqual(["genre"]);
  });

  it("字段按接口里的固定顺序，不是输入的顺序", () => {
    const planned = planChanges(initial, { barcode: { mode: "empty", text: "" }, title: { mode: "custom", text: "甲" } });
    expect(planned.fields).toEqual(["title", "barcode"]);
  });

  it("基线刷新后，动过的字段与最新的基线比较，没动过的字段不会被提交", () => {
    // 我只改了标题；别人在此期间把流派设成了空，基线刷新之后流派仍然不在我的提交里。
    const refreshed: CorrectionStateDTO = {
      ...initial,
      revision: 3,
      fields: initial.fields.map((field) => (field.field === "genre" ? { ...field, correction: { mode: "null" as const }, effective_value: null } : field)),
    };
    const edits: DraftEdits = { title: { mode: "custom", text: "我的标题" } };
    expect(planChanges(refreshed, edits).changes).toEqual({ title: { mode: "set", value: "我的标题" } });
    expect(draftOf(fieldOf(refreshed, "genre"), edits).mode).toBe("empty");
  });
});

describe("confirmScanDraft", () => {
  it("把当前扫描值存为修正；提交的是 set，值等于扫描值", () => {
    const title = fieldOf(initial, "title");
    expect(confirmScanDraft(title)).toEqual({ mode: "custom", text: "Sample Symphonies" });
    expect(planChanges(initial, { title: confirmScanDraft(title) }).changes).toEqual({ title: { mode: "set", value: "Sample Symphonies" } });
    expect(planChanges(initial, { year: confirmScanDraft(fieldOf(initial, "year")) }).changes).toEqual({ year: { mode: "set", value: 1998 } });
  });

  it("扫描值为空时确认的是“空”", () => {
    const missing: CorrectionFieldDTO = { field: "genre", scan_value: null, correction: null, effective_value: null, uncertain: true };
    expect(confirmScanDraft(missing)).toEqual({ mode: "empty", text: "" });
  });
});
