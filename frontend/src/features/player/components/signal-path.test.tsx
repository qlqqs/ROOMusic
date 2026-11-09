import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { playerTrack } from "../../../test/player-data";
import { decodeTrackAnalysis, type TrackAnalysisDTO } from "../api/decoders";
import type { SourceFacts } from "../engine/queue";
import type { PlayerSnapshot } from "../zones/controller";
import type { ZoneStream } from "../zones/model";
import { SignalPathButton } from "./signal-path";

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

/** 只给信号路径用到的字段：当前曲目与正在控制的播放区的渲染流。 */
function viewSource(codec: string, sourceKind: string, stream: ZoneStream | null = null) {
  const source: SourceFacts = { sourceKind, codec, bitDepth: 24, sampleRate: 48000, channels: 2, bitrate: 320 };
  snapshot = { current: { entryId: "entry-1", track: playerTrack("1", { source }) }, zone: { stream } } as unknown as PlayerSnapshot;
  return render(<SignalPathButton />);
}

const flacStream: ZoneStream = { seq: 1, url: "/api/v1/me/playback/zones/z/streams/1", mimeType: "audio/flac", quality: "lossless", format: { codec: "flac", sampleRate: 48000, bitDepth: 24, channels: 2 }, segments: [] };

describe("信号路径编码与质量", () => {
  it.each([
    ["ape", "ape_apev2", "APE", "无损", "lossless", "APE 48kHz 24bit 2ch"],
    ["wavpack", "wavpack_apev2", "WavPack", "无损", "lossless", "WavPack 48kHz 24bit 2ch"],
    ["aiff", "aiff_id3", "AIFF", "无损", "lossless", "AIFF 48kHz 24bit 2ch"],
    ["wavpack_hybrid", "wavpack_apev2", "WavPack", "有损", "lossy", "WavPack 320kbps 48kHz 2ch"],
  ])("%s 按源编码判定质量，数据源用短名，服务端一行是渲染流的格式", async (codec, sourceKind, _name, quality, dotQuality, sourceLine) => {
    const user = userEvent.setup();
    viewSource(codec, sourceKind, flacStream);
    const title = `信号路径：${quality}`;
    const trigger = screen.getByRole("button", { name: title });
    expect(trigger.dataset.quality).toBe(dotQuality);
    await user.click(trigger);
    const dialog = screen.getByRole("dialog", { name: title });
    expect(within(dialog).getAllByRole("listitem").map((row) => row.textContent)).toEqual([
      `数据源${sourceLine}`,
      "服务端FLAC 48kHz 24bit 2ch",
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

  it("没有在放的流时服务端一行写未在播放；省流量档时整体是有损", async () => {
    const user = userEvent.setup();
    const view = viewSource("flac", "flac_vorbis_comment");
    await user.click(screen.getByRole("button", { name: "信号路径：无损" }));
    expect(within(screen.getByRole("dialog", { name: "信号路径：无损" })).getByText("未在播放")).toBeTruthy();
    view.unmount();
    viewSource("flac", "flac_vorbis_comment", { ...flacStream, quality: "lossy", mimeType: 'audio/ogg; codecs="opus"', format: { codec: "opus", sampleRate: 48000, bitDepth: null, channels: 2 } });
    await user.click(screen.getByRole("button", { name: "信号路径：有损" }));
    expect(within(screen.getByRole("dialog", { name: "信号路径：有损" })).getByText("OPUS 48kHz 2ch")).toBeTruthy();
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
      "服务端未在播放",
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
