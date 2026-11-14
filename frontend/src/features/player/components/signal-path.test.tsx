import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { fakeZoneServer, testPlayerController } from "../../../test/fake-zones";
import { playerTrack } from "../../../test/player-data";
import { decodeTrackAnalysis, type TrackAnalysisDTO } from "../api/decoders";
import type { PlayerSnapshot } from "../zones/controller";
import type { Signal } from "../zones/model";
import { SignalPathButton } from "./signal-path";
import styles from "./signal-path.module.css";

vi.mock("../player-provider", () => ({
  usePlayerSelector: function useTestPlayerSelector<T>(select: (snapshot: PlayerSnapshot) => T) {
    return select(snapshot);
  },
}));
vi.mock("../hooks/use-track-analysis", () => ({ useTrackAnalysis: () => analysis }));

let snapshot: PlayerSnapshot;
let analysis: TrackAnalysisDTO["analysis"] = null;
let snapshotStatus: PlayerSnapshot["status"] = "paused";

beforeEach(() => {
  analysis = null;
  snapshotStatus = "paused";
});

const losslessSignal: Signal = {
  quality: "lossless", clipping: false, processingSpeed: 85.3,
  steps: [
    { kind: "source", quality: "lossless", lossy: false, compat: false, params: { codec: "flac", sampleRate: 44100, bitDepth: 16, channels: 2 } },
    { kind: "encode", quality: "lossless", lossy: false, compat: false, params: { codec: "wav", sampleRate: 44100, bitDepth: 16, bitrateKbps: null } },
  ],
};

function viewSignal(signal: Signal | null = losslessSignal, options: { device?: boolean } = {}) {
  const server = fakeZoneServer();
  snapshot = {
    ...testPlayerController(server).controller.getSnapshot(),
    status: snapshotStatus,
    current: { entryId: "entry-1", track: playerTrack("1") },
    zone: { ...server.zone(), signal },
    device: options.device === false ? null : { ...server.device(), name: "客厅 · Chrome" },
  };
  return render(<SignalPathButton />);
}

/** 底部测量格：每格的名称与取值。 */
function measurementsOf(dialog: HTMLElement): [string, string][] {
  return [...dialog.querySelectorAll("dl > div")].map((cell) => [cell.querySelector("dt")?.textContent ?? "", cell.querySelector("dd")?.textContent ?? ""]);
}

function loudnessAnalysis(): NonNullable<TrackAnalysisDTO["analysis"]> {
  const sample = decodeTrackAnalysis(sampleBody("tracks/analysis-done")).analysis;
  if (sample === null) throw new Error("完成的分析样本缺少结果");
  return { ...sample, integratedLufs: -14.24, loudnessRangeLu: 6.26, truePeakDbtp: -0.84, samplePeakDbfs: -3.7 };
}

describe("服务端信号路径", () => {
  it.each([
    ["lossless", "无损"], ["enhanced", "增强"], ["high", "高质量"], ["low", "有损"],
  ] as const)("按钮按服务端 %s 评级选择颜色，不按曲目来源推断", (quality, label) => {
    viewSignal({ ...losslessSignal, quality });
    const trigger = screen.getByRole("button", { name: `FLAC 16/44.1，信号路径：${label}` });
    expect(trigger.textContent).toBe("FLAC 16/44.1");
    expect(trigger.className.split(" ")).toContain(styles.trigger);
    expect(trigger.dataset.quality).toBe(quality);
    expect(trigger.hasAttribute("data-clipping")).toBe(false);
  });

  it("signal 为 null 时不显示按钮，只显示格式文字", () => {
    viewSignal(null);
    expect(screen.queryByRole("button", { name: /信号路径/ })).toBeNull();
    expect(screen.getByText("FLAC 16/44.1", { exact: true })).toBeTruthy();
  });

  it("按步骤顺序显示链：步骤名与取值、兼容转换与有损标记，最后是输出设备；处理速度在测量格，不显示缺失的响度", async () => {
    const user = userEvent.setup();
    viewSignal({
      ...losslessSignal, quality: "low",
      steps: [
        { kind: "source", quality: "lossless", lossy: false, compat: false, params: { codec: "flac", sampleRate: 384000, bitDepth: 24, channels: 2 } },
        { kind: "convert", quality: "high", lossy: false, compat: true, params: { fromRate: 384000, toRate: 192000, by: "ffmpeg" } },
        { kind: "headroom", quality: "enhanced", lossy: false, compat: false, params: { gainDb: -3.1 } },
        { kind: "encode", quality: "low", lossy: true, compat: false, params: { codec: "opus", sampleRate: 48000, bitDepth: null, bitrateKbps: 128 } },
      ],
    });
    await user.click(screen.getByRole("button", { name: "FLAC 16/44.1，信号路径：有损" }));
    const dialog = screen.getByRole("dialog", { name: "信号路径：有损" });
    expect(within(dialog).getByText("FLAC 384 kHz 24 bit → 客厅 · Chrome")).toBeTruthy();
    const rows = within(dialog).getAllByRole("listitem");
    expect(rows.map((row) => row.textContent)).toEqual([
      "来源FLAC 384 kHz 24 bit",
      "采样率转换384 kHz → 192 kHz兼容转换",
      "Headroom−3.1 dB",
      "编码Opus 128 kbps有损",
      "输出客厅 · Chrome",
    ]);
    // 整体是有损：链上第一个有损的一步（编码）决定整体质量，只有它加粗。
    expect(rows.map((row) => row.hasAttribute("data-worst"))).toEqual([false, false, false, true, false]);
    expect(measurementsOf(dialog)).toEqual([["处理速度", "85.3×"]]);
    expect(within(dialog).queryByText(/LUFS|dBTP|dBFS/)).toBeNull();
    expect(within(dialog).queryByText("正在削波")).toBeNull();
  });

  it("DownmixStepShowsChannelLayouts", async () => {
    const user = userEvent.setup();
    viewSignal({
      ...losslessSignal, quality: "high",
      steps: [
        { kind: "downmix", quality: "high", lossy: false, compat: true, params: { fromChannels: 6, toChannels: 2, fromLayout: "5.1", toLayout: "2.0", by: "roomusic", mode: "matrix" } },
      ],
    });
    await user.click(screen.getByRole("button", { name: "FLAC 16/44.1，信号路径：高质量" }));
    const dialog = screen.getByRole("dialog", { name: "信号路径：高质量" });
    expect(within(dialog).getByText("降混", { exact: true })).toBeTruthy();
    expect(within(dialog).getByText("5.1 → 2.0", { exact: true })).toBeTruthy();
  });

  it("SampleRateStepShowsCompatibilityConversion", async () => {
    const user = userEvent.setup();
    viewSignal({
      ...losslessSignal, quality: "high",
      steps: [
        { kind: "source", quality: "lossless", lossy: false, compat: false, params: { codec: "flac", sampleRate: 44100, bitDepth: 16, channels: 2 } },
        { kind: "sample_rate", quality: "high", lossy: false, compat: true, params: { fromRate: 44100, toRate: 48000, filter: "linear_precise" } },
        { kind: "encode", quality: "lossless", lossy: false, compat: false, params: { codec: "wav", sampleRate: 48000, bitDepth: 16, bitrateKbps: null } },
      ],
    });
    await user.click(screen.getByRole("button", { name: "FLAC 16/44.1，信号路径：高质量" }));
    const dialog = screen.getByRole("dialog", { name: "信号路径：高质量" });
    const row = within(dialog).getByText("采样率转换", { exact: true }).closest("li");
    if (row === null) throw new Error("缺少采样率转换这一步");
    expect(row.textContent).toBe("采样率转换44.1 kHz → 48 kHz兼容转换");
    expect(row.hasAttribute("data-worst")).toBe(true);
  });

  it("削波时按钮启用警示色选择器，可访问名称同时标出削波，弹层有“正在削波”", async () => {
    const user = userEvent.setup();
    viewSignal({ ...losslessSignal, clipping: true });
    const trigger = screen.getByRole("button", { name: "FLAC 16/44.1，信号路径：无损，削波" });
    expect(trigger.className.split(" ")).toContain(styles.trigger);
    expect(trigger.hasAttribute("data-clipping")).toBe(true);
    await user.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "信号路径：无损，削波" });
    expect(within(dialog).getByText("正在削波")).toBeTruthy();
    // 整体无损时没有哪一步拉低质量，不加粗。
    expect(within(dialog).getAllByRole("listitem").some((row) => row.hasAttribute("data-worst"))).toBe(false);
  });

  it("圆点只在播放中呼吸，暂停时静止", () => {
    snapshotStatus = "playing";
    const { unmount } = viewSignal();
    expect(screen.getByRole("button", { name: /信号路径/ }).dataset.state).toBe("breathing");
    unmount();
    snapshotStatus = "paused";
    viewSignal();
    expect(screen.getByRole("button", { name: /信号路径/ }).hasAttribute("data-state")).toBe(false);
  });

  it("没有分析结果也没有处理速度时不显示测量格；没有设备时链上没有输出节点", async () => {
    const user = userEvent.setup();
    viewSignal({ ...losslessSignal, processingSpeed: null }, { device: false });
    await user.click(screen.getByRole("button", { name: /信号路径/ }));
    const dialog = screen.getByRole("dialog", { name: "信号路径：无损" });
    expect(dialog.querySelector("dl")).toBeNull();
    expect(within(dialog).queryByText("输出")).toBeNull();
    expect(within(dialog).getByText("FLAC 44.1 kHz 16 bit", { selector: "p" })).toBeTruthy();
  });

  it("分析存在时在来源之后显示一位小数的响度、范围与真峰值，不改变评级", async () => {
    const user = userEvent.setup();
    analysis = loudnessAnalysis();
    viewSignal();
    const trigger = screen.getByRole("button", { name: "FLAC 16/44.1，信号路径：无损" });
    expect(trigger.dataset.quality).toBe("lossless");
    await user.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "信号路径：无损" });
    expect(within(dialog).getAllByRole("listitem").map((row) => row.textContent)).toEqual([
      "来源FLAC 44.1 kHz 16 bit",
      "编码WAV 44.1 kHz 16 bit",
      "输出客厅 · Chrome",
    ]);
    expect(measurementsOf(dialog)).toEqual([["响度", "−14.2 LUFS"], ["范围", "6.3 LU"], ["峰值", "−0.8 dBTP"], ["处理速度", "85.3×"]]);
    expect(within(dialog).queryByText(/dBFS|−3\.7/)).toBeNull();
  });

  it("测量格只列出有的项：分析缺了范围时不显示这一格", async () => {
    const user = userEvent.setup();
    analysis = { ...loudnessAnalysis(), loudnessRangeLu: null };
    viewSignal({ ...losslessSignal, processingSpeed: null });
    await user.click(screen.getByRole("button", { name: /信号路径/ }));
    const dialog = screen.getByRole("dialog", { name: "信号路径：无损" });
    expect(measurementsOf(dialog)).toEqual([["响度", "−14.2 LUFS"], ["峰值", "−0.8 dBTP"]]);
  });
});
