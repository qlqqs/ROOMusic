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

beforeEach(() => {
  analysis = null;
});

const losslessSignal: Signal = {
  quality: "lossless", clipping: false, processingSpeed: 85.3,
  steps: [
    { kind: "source", quality: "lossless", lossy: false, compat: false, params: { codec: "flac", sampleRate: 44100, bitDepth: 16, channels: 2 } },
    { kind: "encode", quality: "lossless", lossy: false, compat: false, params: { codec: "wav", sampleRate: 44100, bitDepth: 16, bitrateKbps: null } },
  ],
};

function viewSignal(signal: Signal | null = losslessSignal) {
  const server = fakeZoneServer();
  snapshot = {
    ...testPlayerController(server).controller.getSnapshot(),
    current: { entryId: "entry-1", track: playerTrack("1") },
    zone: { ...server.zone(), signal },
    device: { ...server.device(), name: "客厅 · Chrome" },
  };
  return render(<SignalPathButton />);
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
    const trigger = screen.getByRole("button", { name: `信号路径：${label}` });
    expect(trigger.className.split(" ")).toContain(styles.trigger);
    expect(trigger.dataset.quality).toBe(quality);
    expect(trigger.hasAttribute("data-clipping")).toBe(false);
  });

  it("signal 为 null 时不显示按钮", () => {
    viewSignal(null);
    expect(screen.queryByRole("button", { name: /信号路径/ })).toBeNull();
  });

  it("按步骤顺序显示兼容转换、有损标记、处理速度与设备，不显示缺失的响度", async () => {
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
    await user.click(screen.getByRole("button", { name: "信号路径：有损" }));
    const dialog = screen.getByRole("dialog", { name: "信号路径：有损" });
    expect(within(dialog).getAllByRole("listitem").map((row) => row.textContent)).toEqual([
      "FLAC 384 kHz 24 bit",
      "采样率 384 kHz → 192 kHz兼容转换",
      "Headroom −3.1 dB",
      "Opus 128 kbps有损",
      "处理速度 85.3×",
      "客厅 · Chrome",
    ]);
    expect(within(dialog).queryByText(/LUFS|dBTP|dBFS/)).toBeNull();
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
    await user.click(screen.getByRole("button", { name: "信号路径：高质量" }));
    const dialog = screen.getByRole("dialog", { name: "信号路径：高质量" });
    expect(within(dialog).getByText("采样率转换 44.1 kHz → 48 kHz")).toBeTruthy();
    expect(within(dialog).getByText("兼容转换")).toBeTruthy();
  });

  it("削波时按钮启用警示色选择器，可访问名称同时标出削波", () => {
    viewSignal({ ...losslessSignal, clipping: true });
    const trigger = screen.getByRole("button", { name: "信号路径：无损，削波" });
    expect(trigger.className.split(" ")).toContain(styles.trigger);
    expect(trigger.hasAttribute("data-clipping")).toBe(true);
  });

  it("分析存在时在来源之后显示一位小数的响度、范围与真峰值，不改变评级", async () => {
    const user = userEvent.setup();
    analysis = loudnessAnalysis();
    viewSignal();
    const trigger = screen.getByRole("button", { name: "信号路径：无损" });
    expect(trigger.dataset.quality).toBe("lossless");
    await user.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "信号路径：无损" });
    expect(within(dialog).getAllByRole("listitem").map((row) => row.textContent)).toEqual([
      "FLAC 44.1 kHz 16 bit",
      "−14.2 LUFS · 范围 6.3 LU · 峰值 −0.8 dBTP",
      "WAV 44.1 kHz 16 bit",
      "处理速度 85.3×",
      "客厅 · Chrome",
    ]);
    expect(within(dialog).queryByText(/dBFS|−3\.7/)).toBeNull();
  });
});
