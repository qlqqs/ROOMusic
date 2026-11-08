import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useSyncExternalStore } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { FakeAudio } from "../../../test/fake-audio";
import { silentPlayRecorder } from "../../../test/play-recorder";
import { playerTrack } from "../../../test/player-data";
import { decodePlaybackFormats } from "../api/decoders";
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

let engine: PlayerEngine;
let table: CapabilityTable;
let capabilitiesPending = false;

beforeEach(async () => {
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
