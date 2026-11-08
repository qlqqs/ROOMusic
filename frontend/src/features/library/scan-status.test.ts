import { describe, expect, it } from "vitest";
import { isScanFinished, scanPhase, scanPhaseLabels, scanPhaseNotes, skippedFileCount, skippedFilesText, type ScanPhase } from "./scan-status";

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

  it("非成功终态说明曲目保留，不否认已保存的变化", () => {
    for (const phase of ["failed", "canceled"] as const) expect(scanPhaseNotes[phase]).toContain("没扫到的曲目保持原样");
    expect(scanPhaseNotes.incomplete).toBe("有目录没扫完，那里的曲目保持原样。");
    expect(scanPhaseNotes.succeeded).toBe("扫描完成。这次没找到的文件已标为缺失。");
    expect(scanPhaseNotes.running).toBeNull();
    expect(scanPhaseNotes.cancel_requested).toBe("正在取消，当前这一步做完就会停下。");
    for (const note of Object.values(scanPhaseNotes)) expect(note ?? "").not.toContain("已有的曲目不受影响");
    for (const note of Object.values(scanPhaseNotes)) expect(note ?? "").not.toMatch(/\d+\s*%/);
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

describe("扫描跳过提示", () => {
  it.each([1, 2, 1234])("成功且跳过 %s 个文件时显示数量", (skipped_files) => {
    expect(skippedFileCount({ status: "succeeded", skipped_files })).toBe(skipped_files);
  });

  it.each([0, null])("成功但跳过数为 %s 时不显示提示", (skipped_files) => {
    expect(skippedFileCount({ status: "succeeded", skipped_files })).toBeNull();
  });

  it.each(["running", "failed", "canceled", "incomplete"] as const)("%s 即使跳过了文件也不显示提示", (status) => {
    expect(skippedFileCount({ status, skipped_files: 2 })).toBeNull();
  });

  it("提示只写跳过数量，数字按界面格式显示", () => {
    expect(skippedFilesText(1)).toBe("跳过 1 个文件");
    expect(skippedFilesText(1234)).toBe("跳过 1,234 个文件");
  });
});
