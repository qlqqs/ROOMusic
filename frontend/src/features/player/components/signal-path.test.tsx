import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useSyncExternalStore } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { FakeAudio } from "../../../test/fake-audio";
import { silentPlayRecorder } from "../../../test/play-recorder";
import { playerTrack } from "../../../test/player-data";
import { decodePlaybackFormats, decodeTrackAnalysis, type TrackAnalysisDTO } from "../api/decoders";
import { buildCapabilityTable, type CapabilityTable } from "../engine/capability";
import { PlayerEngine, type PlayerSnapshot } from "../engine/player-engine";
import type { SourceFacts } from "../engine/queue";
import { SignalPathButton } from "./signal-path";

vi.mock("../player-provider", () => ({
  usePlayerSelector: function useTestPlayerSelector<T>(select: (snapshot: PlayerSnapshot) => T) {
    return useSyncExternalStore(engine.subscribe, () => select(engine.getSnapshot()));
  },
}));
vi.mock("../hooks/use-playback-capabilities", () => ({
  usePlaybackCapabilities: () => ({ data: capabilitiesPending ? undefined : table, isPending: capabilitiesPending }),
}));
vi.mock("../hooks/use-track-analysis", () => ({ useTrackAnalysis: () => analysis }));

let engine: PlayerEngine;
let table: CapabilityTable;
let capabilitiesPending = false;
let analysis: TrackAnalysisDTO["analysis"] = null;

beforeEach(async () => {
  analysis = null;
  capabilitiesPending = false;
  // 交付方式取自后端样本，仅将浏览器能力探针设为支持。
  table = await buildCapabilityTable(decodePlaybackFormats(sampleBody("playback/formats")).items, { canPlayType: () => "probably" });
  engine = new PlayerEngine({
    userId: "test-user", audio: new FakeAudio(),
    loadPlayback: async () => { throw new Error("信号路径展示不应发起播放"); },
    loadRelease: async () => { throw new Error("没有保存的队列"); },
    shouldPrefetch: () => false,
    sessionStorage: null, localStorage: null, mediaSession: null,
    artworkUrl: (id) => id, newId: () => "entry-1", random: () => 0, now: () => 0,
    playRecorder: silentPlayRecorder,
  });
});
afterEach(() => engine.endSession());

function viewSource(codec: string, sourceKind: string) {
  const source: SourceFacts = { sourceKind, codec, bitDepth: 24, sampleRate: 48000, channels: 2, bitrate: 320 };
  engine.enqueue([playerTrack("1", { source })]);
  return render(<SignalPathButton />);
}

describe("信号路径编码与质量", () => {
  it.each([
    ["ape", "ape_apev2", "APE", "无损", "lossless", "APE 48kHz 24bit 2ch"],
    ["wavpack", "wavpack_apev2", "WavPack", "无损", "lossless", "WavPack 48kHz 24bit 2ch"],
    ["aiff", "aiff_id3", "AIFF", "无损", "lossless", "AIFF 48kHz 24bit 2ch"],
    ["wavpack_hybrid", "wavpack_apev2", "WavPack", "有损", "lossy", "WavPack 320kbps 48kHz 2ch"],
  ])("%s 按源编码判定质量，两行使用短名且只在服务端标记派生", async (codec, sourceKind, name, quality, dotQuality, sourceLine) => {
    const user = userEvent.setup();
    viewSource(codec, sourceKind);
    const title = `信号路径：${quality}`;
    const trigger = screen.getByRole("button", { name: title });
    expect(trigger.dataset.quality).toBe(dotQuality);
    await user.click(trigger);
    const dialog = screen.getByRole("dialog", { name: title });
    expect(within(dialog).getAllByRole("listitem").map((row) => row.textContent)).toEqual([
      `数据源${sourceLine}`,
      `服务端${name} → FLAC`,
      "浏览器解码",
      "系统输出系统默认输出",
    ]);
  });

  it.each([
    ["flac", "flac_vorbis_comment", "无损", "lossless"],
    ["alac", "m4a", "无损", "lossless"],
    ["pcm", "wav_info", "无损", "lossless"],
    ["mp3", "mp3_id3v2", "有损", "lossy"],
    ["aac", "m4a", "有损", "lossy"],
    ["vorbis", "ogg_vorbis_comment", "有损", "lossy"],
    ["opus", "opus_tags", "有损", "lossy"],
  ])("已有编码 %s 的质量判定保持不变", (codec, sourceKind, quality, dotQuality) => {
    viewSource(codec, sourceKind);
    expect(screen.getByRole("button", { name: `信号路径：${quality}` }).dataset.quality).toBe(dotQuality);
  });

  it.each([
    ["wavpack_float", "wavpack_apev2"],
    ["wavpack_dsd", "wavpack_apev2"],
    ["aiff_float", "aiff_id3"],
    ["aifc_ima4", "aiff_id3"],
  ])("%s 不因容器属于无损格式就被标成无损", async (codec, sourceKind) => {
    const user = userEvent.setup();
    viewSource(codec, sourceKind);
    const trigger = screen.getByRole("button", { name: "信号路径" });
    expect(trigger.dataset.quality).toBe("unknown");
    await user.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "信号路径" });
    expect(within(dialog).getByText(`${codec.toUpperCase()} 48kHz 24bit 2ch`)).toBeTruthy();
  });

  it("新格式在格式表尚未取得时显示读取中，不冒称原文件直出", async () => {
    const user = userEvent.setup();
    capabilitiesPending = true;
    viewSource("wavpack", "wavpack_apev2");
    await user.click(screen.getByRole("button", { name: "信号路径：无损" }));
    const dialog = screen.getByRole("dialog", { name: "信号路径：无损" });
    expect(within(dialog).getByText("读取中")).toBeTruthy();
    expect(within(dialog).queryByText("原文件直出")).toBeNull();
  });
});

function loudnessAnalysis(overrides: Partial<NonNullable<TrackAnalysisDTO["analysis"]>> = {}): NonNullable<TrackAnalysisDTO["analysis"]> {
  const sample = decodeTrackAnalysis(sampleBody("tracks/analysis-done")).analysis;
  if (sample === null) throw new Error("完成的分析样本缺少结果");
  return { ...sample, integratedLufs: -14.24, loudnessRangeLu: 6.26, truePeakDbtp: -0.84, samplePeakDbfs: -3.7, ...overrides };
}

describe("信号路径响度", () => {
  it("在数据源与服务端之间显示一位小数的响度、范围与真峰值，不显示采样峰值", async () => {
    const user = userEvent.setup();
    analysis = loudnessAnalysis();
    viewSource("flac", "flac_vorbis_comment");
    await user.click(screen.getByRole("button", { name: "信号路径：无损" }));
    const dialog = screen.getByRole("dialog", { name: "信号路径：无损" });
    expect(within(dialog).getAllByRole("listitem").map((row) => row.textContent)).toEqual([
      "数据源FLAC 48kHz 24bit 2ch",
      "响度−14.2 LUFS · 范围 6.3 LU · 峰值 −0.8 dBTP",
      "服务端原文件直出",
      "浏览器解码",
      "系统输出系统默认输出",
    ]);
    expect(within(dialog).queryByText(/dBFS|−3\.7/)).toBeNull();
  });

  it.each([
    [{ integratedLufs: null }, "范围 6.3 LU · 峰值 −0.8 dBTP"],
    [{ loudnessRangeLu: null }, "−14.2 LUFS · 峰值 −0.8 dBTP"],
    [{ truePeakDbtp: null }, "−14.2 LUFS · 范围 6.3 LU"],
    [{ integratedLufs: null, loudnessRangeLu: 0, truePeakDbtp: null }, "范围 0.0 LU"],
  ] as const)("缺项时省略对应数值与分隔符 %#", async (overrides, expected) => {
    const user = userEvent.setup();
    analysis = loudnessAnalysis(overrides);
    viewSource("flac", "flac_vorbis_comment");
    await user.click(screen.getByRole("button", { name: "信号路径：无损" }));
    const dialog = screen.getByRole("dialog", { name: "信号路径：无损" });
    expect(within(dialog).getAllByRole("listitem").map((row) => row.textContent)).toContain(`响度${expected}`);
  });

  it.each(["没有结果", "三项全为 null"] as const)("%s 时整行不存在，即使有采样峰值", async (state) => {
    const user = userEvent.setup();
    analysis = state === "没有结果" ? null : loudnessAnalysis({ integratedLufs: null, loudnessRangeLu: null, truePeakDbtp: null });
    viewSource("flac", "flac_vorbis_comment");
    await user.click(screen.getByRole("button", { name: "信号路径：无损" }));
    const dialog = screen.getByRole("dialog", { name: "信号路径：无损" });
    expect(within(dialog).queryByText("响度")).toBeNull();
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(4);
    expect(within(dialog).queryByText(/LUFS|dBTP|dBFS/)).toBeNull();
  });
});
