import { describe, expect, it } from "vitest";
import { isScanFinished, scanPhase, scanPhaseLabels, scanPhaseNotes, type ScanPhase } from "./scan-status";

describe("scanPhase", () => {
  it("运行中且没有取消请求是 running，有取消请求是 cancel_requested", () => {
    expect(scanPhase({ status: "running", cancel_requested_at: null })).toBe("running");
    expect(scanPhase({ status: "running", cancel_requested_at: "2026-01-01T00:00:01Z" })).toBe("cancel_requested");
  });

  it.each(["succeeded", "failed", "canceled", "incomplete"] as const)("终态 %s 原样保留，即使带有取消请求时间", (status) => {
    expect(scanPhase({ status, cancel_requested_at: null })).toBe(status);
    expect(scanPhase({ status, cancel_requested_at: "2026-01-01T00:00:01Z" })).toBe(status);
  });

  it("六种阶段的文案互不相同", () => {
    const phases: ScanPhase[] = ["running", "cancel_requested", "succeeded", "failed", "canceled", "incomplete"];
    expect(new Set(phases.map((phase) => scanPhaseLabels[phase])).size).toBe(6);
    expect(new Set(phases.map((phase) => scanPhaseNotes[phase])).size).toBe(6);
  });

  it("失败、已取消、不完整都说明已有的曲目不受影响，运行中不暗示进度", () => {
    for (const phase of ["failed", "canceled", "incomplete"] as const) expect(scanPhaseNotes[phase]).toContain("已有的曲目不受影响");
    expect(scanPhaseNotes.succeeded).not.toContain("已有的曲目不受影响");
    for (const note of Object.values(scanPhaseNotes)) expect(note).not.toMatch(/\d+\s*%/);
  });
});

describe("isScanFinished", () => {
  it("只有 running 没有结束", () => {
    expect(isScanFinished({ status: "running" })).toBe(false);
    for (const status of ["succeeded", "failed", "canceled", "incomplete"] as const) {
      expect(isScanFinished({ status })).toBe(true);
    }
  });
});
