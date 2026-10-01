import { describe, expect, it } from "vitest";
import { sampleBody } from "../../test/api-samples";
import { decodeDiagnosticsPage } from "./api/decoders";
import { diagnosticKindLabel, isKnownDiagnosticKind } from "./diagnostic-kinds";

describe("diagnosticKindLabel", () => {
  it("已知类别显示中文名", () => {
    expect(diagnosticKindLabel("parse_failure")).toBe("音频解析失败");
    expect(diagnosticKindLabel("unknown")).toBe("未知类别");
  });

  it("未知类别原样显示，且不被对象原型上的名字误认", () => {
    expect(diagnosticKindLabel("brand_new_kind")).toBe("brand_new_kind");
    expect(diagnosticKindLabel("constructor")).toBe("constructor");
    expect(isKnownDiagnosticKind("constructor")).toBe(false);
  });

  it("后端样本里出现的类别都有中文名", () => {
    for (const name of ["scans/diagnostics", "scans/diagnostics-multiple-kinds"]) {
      const page = decodeDiagnosticsPage(sampleBody(name));
      for (const { kind } of page.summary.counts) expect(isKnownDiagnosticKind(kind), kind).toBe(true);
      for (const { kind } of page.items) expect(isKnownDiagnosticKind(kind), kind).toBe(true);
    }
  });
});
