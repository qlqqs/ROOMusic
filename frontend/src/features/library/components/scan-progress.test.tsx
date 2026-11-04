import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { without } from "../../../test/mutate";
import { decodeScan, type ScanDTO, type ScanProgressDTO } from "../api/decoders";
import { ScanProgress } from "./scan-progress";

const scanSample = (name: string) => decodeScan(sampleBody(`scan-feedback/${name}`));
function withProgress(scan: ScanDTO, patch: Partial<ScanProgressDTO>): ScanDTO {
  if (scan.progress === undefined || scan.progress === null) throw new Error("样本缺少进度");
  return { ...scan, progress: { ...scan.progress, ...patch } };
}
const progressbar = () => screen.getByRole("progressbar", { name: "扫描进度" });

afterEach(() => vi.useRealTimers());

describe("扫描进度的可见与读屏语义", () => {
  it("按服务端基点向下取整，读屏包含目录、阶段和全局文件累计", () => {
    render(<ScanProgress scan={scanSample("second-root-reading")} />);
    const bar = progressbar();
    expect(bar.getAttribute("aria-valuemin")).toBe("0");
    expect(bar.getAttribute("aria-valuemax")).toBe("100");
    expect(bar.getAttribute("aria-valuenow")).toBe("49");
    expect(bar.getAttribute("aria-valuetext")).toBe("第 2 / 共 2 个目录，读取标签，已处理 250 / 400 个文件，整体进度 49%");
    expect(screen.getByText("49%")).toBeTruthy();
    expect(screen.queryByRole("slider")).toBeNull();
    expect(bar.hasAttribute("tabindex")).toBe(false);
    expect(document.activeElement).not.toBe(bar);
  });

  it("数文件的完成数使用目录单位，不拿已发现文件冒充处理进度", () => {
    const scan = withProgress(scanSample("counting-admin"), { completed: 1, files_counted: 1234 });
    render(<ScanProgress scan={scan} />);
    expect(progressbar().getAttribute("aria-valuetext")).toBe("正在数文件，已检查 1 / 2 个目录，已数到 1,234 个文件，整体进度 0%");
    expect(screen.getByText("已数到 1,234 个文件")).toBeTruthy();
    expect(screen.queryByText(/已处理/)).toBeNull();
  });

  it("全部目录结束后的 saving 不捏造当前目录，显示保存扫描结果", () => {
    const scan = withProgress(scanSample("saving-admin"), { percent_bp: 9900, completed: 2 });
    render(<ScanProgress scan={{ ...scan, root_progress: { index: null, total: 2, id: null, name: null } }} showRoot />);
    expect(progressbar().getAttribute("aria-valuetext")).toBe("保存扫描结果，已处理 100 / 400 个文件，整体进度 99%");
    expect(screen.queryByText(/当前目录：|第 \d+ \/ 共/)).toBeNull();
  });

  it("计数后增添文件 A>T 时保留原总数，改用计数时文案而非超额比例", () => {
    const scan = withProgress(scanSample("reading_tags-admin"), { files_processed: 1235, files_total: 1200, files_changed: true, percent_bp: 9980 });
    render(<ScanProgress scan={scan} />);
    expect(screen.getByText("已处理 1,235 个文件（计数时 1,200 个）")).toBeTruthy();
    expect(progressbar().getAttribute("aria-valuetext")).toContain("已处理 1,235 个文件（计数时 1,200 个）");
    expect(progressbar().getAttribute("aria-valuenow")).toBe("99");
    expect(screen.queryByText(/1,235 \/ 1,200/)).toBeNull();
  });

  it("处理阶段总数未知时只展示已处理文件，不编造分母", () => {
    render(<ScanProgress scan={withProgress(scanSample("reading_tags-admin"), { files_total: null, count_complete: false })} />);
    expect(screen.getByText("已处理 50 个文件")).toBeTruthy();
    expect(progressbar().getAttribute("aria-valuetext")).not.toContain("50 /");
  });

  it.each(["legacy", "missing"] as const)("%s 数据运行中使用无确定数值的准备状态，终态隐藏进度", (kind) => {
    const scan = kind === "legacy" ? scanSample("legacy") : decodeScan(without(without(without(sampleBody("scan-feedback/legacy"), ["progress"]), ["root_progress"]), ["report_status"]));
    const { rerender } = render(<ScanProgress scan={{ ...scan, status: "running", finished_at: null }} />);
    expect(progressbar().hasAttribute("aria-valuenow")).toBe(false);
    expect(progressbar().getAttribute("aria-valuetext")).toBe("正在准备");
    expect(screen.queryByText(/\d+%/)).toBeNull();
    rerender(<ScanProgress scan={{ ...scan, status: "succeeded" }} />);
    expect(screen.queryByRole("progressbar")).toBeNull();
  });

  it("管理员详情显示目录名与明确的局部文件数，概览不显示名称", () => {
    const scan = scanSample("reading_tags-admin");
    const { rerender } = render(<ScanProgress scan={scan} />);
    expect(screen.queryByText(/测试音乐甲/)).toBeNull();
    rerender(<ScanProgress scan={scan} showRoot />);
    expect(screen.getByText("当前目录：测试音乐甲（这个目录 50 / 100 个文件）")).toBeTruthy();
    expect(progressbar().getAttribute("aria-valuetext")).toContain("已处理 50 / 400 个文件");
    rerender(<ScanProgress scan={scanSample("reading_tags-member")} showRoot />);
    expect(screen.queryByText(/当前目录：|测试音乐甲/)).toBeNull();
  });

  it("同阶段只增加文件数不重复播报，换根即使仍读取标签也播报", () => {
    const scan = scanSample("reading_tags-admin");
    const { container, rerender } = render(<ScanProgress scan={scan} />);
    const liveText = () => container.querySelector('[aria-live="polite"]')?.textContent;
    expect(liveText()).toBe("第 1 / 共 2 个目录，读取标签");
    rerender(<ScanProgress scan={withProgress(scan, { completed: 75, files_processed: 75, percent_bp: 1700 })} />);
    expect(liveText()).toBe("第 1 / 共 2 个目录，读取标签");
    rerender(<ScanProgress scan={scanSample("second-root-started")} />);
    expect(liveText()).toBe("第 2 / 共 2 个目录，读取标签");
  });

  it("按权威快照跨根 saving→reading_tags 单调展示，不因局部分子重置倒退", () => {
    const names = ["counting-admin", "reading_tags-admin", "organizing-admin", "saving-admin", "first-root-saved", "second-root-started", "second-root-reading", "terminal-succeeded"];
    const { rerender } = render(<ScanProgress scan={scanSample("counting-admin")} />);
    const displayed = names.map((name) => {
      rerender(<ScanProgress scan={scanSample(name)} />);
      return Number(progressbar().getAttribute("aria-valuenow"));
    });
    expect(displayed).toEqual([0, 12, 19, 23, 28, 28, 49, 100]);
    expect(displayed).toEqual([...displayed].sort((left, right) => left - right));
  });

  it.each(["succeeded", "failed", "canceled", "incomplete"] as const)("%s 终态停在服务端快照，经过时间也不补满", (status) => {
    vi.useFakeTimers();
    const { container } = render(<ScanProgress scan={scanSample(`terminal-${status}`)} showRoot />);
    const expected = status === "succeeded" ? "100" : "49";
    expect(progressbar().getAttribute("aria-valuenow")).toBe(expected);
    expect(container.querySelector('[aria-live="polite"]')).toBeNull();
    if (status !== "succeeded") {
      expect(progressbar().getAttribute("aria-valuetext")).toContain("停在第 2 / 共 2 个目录，读取标签");
      expect(screen.queryByText("100%")).toBeNull();
    }
    act(() => vi.advanceTimersByTime(60_000));
    expect(progressbar().getAttribute("aria-valuenow")).toBe(expected);
  });

  it.each(["running", "failed", "canceled", "incomplete"] as const)("%s 即使快照达到 10000 基点也最多显示 99%", (status) => {
    const scan = withProgress(scanSample("reading_tags-admin"), { percent_bp: 10000 });
    render(<ScanProgress scan={{ ...scan, status }} />);
    expect(progressbar().getAttribute("aria-valuenow")).toBe("99");
    expect(screen.queryByText("100%")).toBeNull();
  });

  it("请求取消仍显示最新进度，不把意图当终态", () => {
    const scan = scanSample("reading_tags-admin");
    const { container } = render(<ScanProgress scan={{ ...scan, cancel_requested_at: "2026-01-01T00:00:02Z" }} />);
    expect(progressbar().getAttribute("aria-valuenow")).toBe("12");
    expect(progressbar().getAttribute("aria-valuetext")).not.toContain("停在");
    expect(container.querySelector('[aria-live="polite"]')?.textContent).toContain("读取标签");
  });
});
