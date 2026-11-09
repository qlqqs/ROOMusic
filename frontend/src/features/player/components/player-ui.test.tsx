import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useSyncExternalStore, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import signalEvent from "../../../../../backend/internal/app/testdata/api-samples/playback-zones/events/zone-signal.json";
import { albumGroup, releaseDetailOf } from "../../../test/catalog-data";
import { FakeAudio } from "../../../test/fake-audio";
import { fakeZoneServer, testPlayerController, type FakeZoneServer } from "../../../test/fake-zones";
import { playerTrack } from "../../../test/player-data";
import type { PlayerController, PlayerProgress, PlayerSnapshot } from "../zones/controller";
import { decodeStreamEvent } from "../zones/decoders";
import type { PlayerTrack } from "../engine/queue";
import type { CapabilityTable } from "../engine/capability";
import { AlbumPlaybackActions } from "./album-playback-actions";
import { FullscreenPlayer } from "./fullscreen-player";
import { PlayerBar } from "./player-bar";
import { PlayerQueue } from "./player-queue";
import { PlayerAnnouncement } from "./player-feedback";
import { SignalPathButton } from "./signal-path";
import { WaveformSeek } from "./waveform-seek";
import { PlaybackSettingsForm } from "./playback-settings-form";
import { decodeTrackAnalysis } from "../api/decoders";
import { sampleBody } from "../../../test/api-samples";

const fullscreenState = vi.hoisted(() => ({ open: false, listeners: new Set<() => void>() }));
const analysisState = vi.hoisted(() => ({ value: null as unknown }));

vi.mock("@tanstack/react-router", () => ({
  Link: ({ to, children, hash, title }: { to: string; children: ReactNode; hash?: string; title?: string }) => <a href={`${to}${hash === undefined ? "" : `#${hash}`}`} title={title}>{children}</a>,
  useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) => select({ location: { pathname: "/" } }),
}));
vi.mock("../hooks/use-fullscreen-player", async () => {
  const { useSyncExternalStore: subscribe } = await import("react");
  const publish = (open: boolean) => {
    fullscreenState.open = open;
    fullscreenState.listeners.forEach((listener) => listener());
  };
  return {
    fullscreenOrigin: () => null,
    useFullscreenPlayer: () => ({
      open: subscribe((listener) => { fullscreenState.listeners.add(listener); return () => fullscreenState.listeners.delete(listener); }, () => fullscreenState.open),
      openPlayer: () => publish(true),
      closePlayer: () => publish(false),
    }),
  };
});
vi.mock("../hooks/use-track-analysis", () => ({
  useTrackAnalysis: () => analysisState.value,
}));
vi.mock("../hooks/use-track-lyrics", () => ({
  useTrackLyrics: function useTestLyrics() { return { status: "success", data: null, error: null, refetch: () => undefined }; },
}));
vi.mock("../player-provider", () => ({
  usePlayer: function useTestPlayer() { return { engine, snapshot: useSyncExternalStore(engine.subscribe, engine.getSnapshot) }; },
  usePlayerEngine: function useTestPlayerEngine() { return engine; },
  usePlayerSelector: function useTestPlayerSelector<T>(select: (snapshot: PlayerSnapshot) => T) { return useSyncExternalStore(engine.subscribe, () => select(engine.getSnapshot())); },
  usePlayerProgress: function useTestProgress() { return useSyncExternalStore(engine.subscribeProgress, engine.getProgress); },
  usePlayerProgressSelector: function useTestProgressSelector<T>(select: (progress: PlayerProgress) => T) { return useSyncExternalStore(engine.subscribeProgress, () => select(engine.getProgress())); },
}));
vi.mock("../hooks/use-playback-capabilities", () => ({
  usePlaybackCapabilities: function useTestCapabilities() {
    return useQuery({ queryKey: ["test-capabilities"], queryFn: () => Promise.resolve(table), initialData: table });
  },
}));
let engine: PlayerController;
let server: FakeZoneServer;
let audios: FakeAudio[];
let playing: () => FakeAudio | undefined;
let table: CapabilityTable;
function view(children: ReactNode) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>);
}
function start(options: { lockedVolume?: boolean } = {}) {
  server = fakeZoneServer();
  const test = testPlayerController(server, { audio: true });
  engine = test.controller;
  audios = test.audios;
  playing = test.playing;
  if (options.lockedVolume === true) {
    const createAudio = () => {
      const audio = new FakeAudio();
      Object.defineProperty(audio, "volume", { configurable: true, get: () => 1, set: () => undefined });
      audios.push(audio);
      return audio;
    };
    engine = testPlayerController(server, { deps: { createAudio } }).controller;
  }
  engine.attach();
}
/** 发命令并等服务端的响应回到快照。 */
async function enqueue(tracks: PlayerTrack[]) {
  const before = engine.getSnapshot().queue.entries.length;
  act(() => { engine.enqueue(tracks); });
  await waitFor(() => expect(engine.getSnapshot().queue.entries).toHaveLength(before + tracks.length));
}
/** 当前流的元素开始出声。 */
async function sounding(): Promise<FakeAudio> {
  await waitFor(() => expect(playing()).toBeDefined());
  const audio = playing();
  if (audio === undefined) throw new Error("没有在放的元素");
  act(() => audio.beginPlaying());
  return audio;
}
beforeEach(() => {
  fullscreenState.open = false;
  analysisState.value = null;
  fullscreenState.listeners.clear();
  table = { verdictOf: ({ codec }) => codec === "alac" ? { playable: false, reason: "format_unsupported" } : { playable: true } };
  start();
});
afterEach(() => { engine.endSession(); vi.useRealTimers(); });

describe("播放栏、全屏、队列与信号路径", () => {
  it("播放栏封面打开全屏；Esc 和收起按钮都关闭全屏", async () => {
    const user = userEvent.setup();
    await enqueue([playerTrack("1")]);
    view(<><PlayerBar /><FullscreenPlayer /></>);
    await user.click(screen.getByRole("button", { name: "打开全屏播放器" }));
    let dialog = await screen.findByRole("dialog", { name: "曲目 1" });
    expect(dialog.hasAttribute("open")).toBe(true);
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "收起全屏播放器" }));
    for (const removedText of ["内嵌歌词 · 逐行同步 · 点任一行跳到该处", "内嵌歌词 · 逐行同步", "内嵌歌词 · 纯文本"]) expect(dialog.textContent).not.toContain(removedText);
    fireEvent(dialog, new Event("cancel", { bubbles: true, cancelable: true }));
    await waitFor(() => expect(dialog.hasAttribute("open")).toBe(false));

    await user.click(screen.getByRole("button", { name: "打开全屏播放器" }));
    dialog = await screen.findByRole("dialog", { name: "曲目 1" });
    await user.click(screen.getByRole("button", { name: "收起全屏播放器" }));
    await waitFor(() => expect(dialog.hasAttribute("open")).toBe(false));
  });

  it("分析波形保留可键盘调整且可访问名称不变；无分析时用普通进度条", async () => {
    act(() => { engine.playTracks([playerTrack("1")]); });
    await sounding();
    analysisState.value = decodeTrackAnalysis(sampleBody("tracks/analysis-done")).analysis;
    const rendered = view(<WaveformSeek />);
    expect(document.querySelector("canvas")).toBeTruthy();
    const range = screen.getByRole("slider", { name: "播放进度" });
    expect((range as HTMLInputElement).disabled).toBe(false);
    fireEvent.change(range, { target: { value: "1" } });
    await waitFor(() => expect(server.commands.at(-1)).toEqual({ command: "seek", position_seconds: 1 }));
    rendered.unmount();
    analysisState.value = null;
    view(<WaveformSeek />);
    expect(document.querySelector("canvas")).toBeNull();
    expect(screen.getAllByRole("slider", { name: "播放进度" }).length).toBeGreaterThan(0);
  });

  it("播放设置分段选择发给播放区", async () => {
    const user = userEvent.setup();
    await waitFor(() => expect(engine.getSnapshot().zone).not.toBeNull());
    view(<PlaybackSettingsForm />);
    await user.click(screen.getByRole("radio", { name: "按专辑" }));
    await waitFor(() => expect(engine.getSnapshot().settings.leveling).toBe("album"));
    expect(server.commands.at(-1)).toEqual({ command: "set_settings", settings: { leveling: "album" } });
  });

  it("Headroom 与削波开关各发送一次完整设置，保留其他 Headroom 字段", async () => {
    const user = userEvent.setup();
    await waitFor(() => expect(engine.getSnapshot().zone).not.toBeNull());
    view(<PlaybackSettingsForm />);
    expect((screen.getByRole("slider", { name: "增益" }) as HTMLInputElement).disabled).toBe(true);
    await user.click(screen.getByRole("switch", { name: "Headroom" }));
    await waitFor(() => expect(engine.getSnapshot().settings.headroom.enabled).toBe(true));
    expect(server.commands).toEqual([
      { command: "set_settings", settings: { headroom: { enabled: true, db: -3, clip_indicator: false } } },
    ]);
    expect((screen.getByRole("slider", { name: "增益" }) as HTMLInputElement).disabled).toBe(false);
    await user.click(screen.getByRole("switch", { name: "削波指示" }));
    await waitFor(() => expect(engine.getSnapshot().settings.headroom.clipIndicator).toBe(true));
    expect(server.commands).toEqual([
      { command: "set_settings", settings: { headroom: { enabled: true, db: -3, clip_indicator: false } } },
      { command: "set_settings", settings: { headroom: { enabled: true, db: -3, clip_indicator: true } } },
    ]);
  });

  it("SampleRateSettingsSubmitCompleteDocuments", async () => {
    const user = userEvent.setup();
    await waitFor(() => expect(engine.getSnapshot().zone).not.toBeNull());
    view(<PlaybackSettingsForm />);
    await user.selectOptions(screen.getByRole("combobox", { name: "采样率转换" }), "指定采样率");
    await waitFor(() => expect(engine.getSnapshot().settings.sampleRate).toEqual({ strategy: "custom", customRate: 96000, filter: "linear_precise" }));
    expect(server.commands).toEqual([
      { command: "set_settings", settings: { sample_rate: { strategy: "custom", custom_rate: 96000, filter: "linear_precise" } } },
    ]);

    await user.selectOptions(screen.getByRole("combobox", { name: "转换滤波器" }), "精确 · 最小相位");
    await waitFor(() => expect(engine.getSnapshot().settings.sampleRate.filter).toBe("minimum_precise"));
    expect(server.commands).toEqual([
      { command: "set_settings", settings: { sample_rate: { strategy: "custom", custom_rate: 96000, filter: "linear_precise" } } },
      { command: "set_settings", settings: { sample_rate: { strategy: "custom", custom_rate: 96000, filter: "minimum_precise" } } },
    ]);
  });

  it("Headroom 拖动只预览一位小数，松手提交一次，紧接的失焦不重复提交", async () => {
    await waitFor(() => expect(engine.getSnapshot().zone).not.toBeNull());
    server.afterNext({ settings: { ...server.zone().settings, headroom: { enabled: true, db: -3, clipIndicator: true } } });
    await enqueue([playerTrack("1")]);
    server.commands.length = 0;
    view(<PlaybackSettingsForm />);
    const slider = screen.getByRole("slider", { name: "增益" });
    expect(slider.getAttribute("min")).toBe("-30");
    expect(slider.getAttribute("max")).toBe("0");
    expect(slider.getAttribute("step")).toBe("0.1");
    fireEvent.pointerDown(slider);
    fireEvent.change(slider, { target: { value: "-4.2" } });
    fireEvent.change(slider, { target: { value: "-4.3" } });
    expect(slider.getAttribute("aria-valuetext")).toBe("−4.3 dB");
    expect(screen.getByText("−4.3 dB")).toBeTruthy();
    expect(server.commands).toEqual([]);
    await act(async () => {
      fireEvent.pointerUp(slider);
      fireEvent.blur(slider);
    });
    expect(server.commands).toEqual([
      { command: "set_settings", settings: { headroom: { enabled: true, db: -4.3, clip_indicator: true } } },
    ]);
  });

  it.each(["按键抬起", "失焦"] as const)("Headroom 滑块在%s时提交尚未发送的增益", async (commit) => {
    await waitFor(() => expect(engine.getSnapshot().zone).not.toBeNull());
    server.afterNext({ settings: { ...server.zone().settings, headroom: { enabled: true, db: -3, clipIndicator: false } } });
    await enqueue([playerTrack("1")]);
    server.commands.length = 0;
    view(<PlaybackSettingsForm />);
    const slider = screen.getByRole("slider", { name: "增益" });
    fireEvent.focus(slider);
    fireEvent.keyDown(slider, { key: "ArrowRight" });
    // jsdom 不执行原生 range 的按键步进，用 change 模拟浏览器产生的新值。
    fireEvent.change(slider, { target: { value: "-2.9" } });
    expect(server.commands).toEqual([]);
    await act(async () => {
      if (commit === "按键抬起") fireEvent.keyUp(slider, { key: "ArrowRight" });
      else fireEvent.blur(slider);
    });
    expect(server.commands).toEqual([
      { command: "set_settings", settings: { headroom: { enabled: true, db: -2.9, clip_indicator: false } } },
    ]);
  });

  it("设备不能调音量时音量控件禁用，悬停说明原因", async () => {
    engine.endSession();
    start({ lockedVolume: true });
    await waitFor(() => expect(engine.getSnapshot().volumeControllable).toBe(false));
    view(<PlayerBar />);
    expect((screen.getByRole("slider", { name: "播放器音量" }) as HTMLInputElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "静音" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByRole("slider", { name: "播放器音量" }).closest("[title]")?.getAttribute("title")).toBe("这台设备不能调音量");
  });

  it("队列抽屉关闭后焦点返回队列图标", async () => {
    const user = userEvent.setup();
    await enqueue([playerTrack("1"), playerTrack("2")]);
    view(<PlayerBar />);
    const opener = screen.getByRole("button", { name: "队列（2）" });
    await user.click(opener);
    const dialog = await screen.findByRole("dialog", { name: "队列（2）" });
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(2);
    await user.click(within(dialog).getByRole("button", { name: "关闭队列" }));
    await waitFor(() => expect(dialog.hasAttribute("open")).toBe(false));
    expect(document.activeElement).toBe(opener);
  });

  it("信号事件经控制器展示处理步骤，不再从曲目元数据拼出四层路径", async () => {
    const user = userEvent.setup();
    const event = decodeStreamEvent(signalEvent.event, JSON.stringify(signalEvent.data));
    if (event?.type !== "zone") throw new Error("信号样本未解码为播放区事件");
    server.afterNext({ signal: event.zone.signal });
    act(() => { engine.playTracks([playerTrack("1", { source: { sourceKind: "m4a", codec: "alac", bitDepth: 24, sampleRate: 48000, channels: 2, bitrate: null } })]); });
    await waitFor(() => expect(engine.getSnapshot().zone?.signal).toEqual(event.zone.signal));
    view(<SignalPathButton />);
    await user.click(screen.getByRole("button", { name: "信号路径：增强，削波" }));
    const dialog = await screen.findByRole("dialog", { name: "信号路径：增强，削波" });
    expect(within(dialog).getAllByRole("listitem").map((row) => row.textContent)).toEqual([
      "WAV 48 kHz 16 bit",
      "Headroom −3.1 dB",
      "TPDF 抖动 16 bit",
      "WAV 48 kHz 16 bit",
      "处理速度 85.3×",
      "测试设备",
    ]);
  });

  it("渲染器暂时不可用时显示可重试状态，重试后恢复", async () => {
    await enqueue([playerTrack("1")]);
    view(<><PlayerBar /><PlayerAnnouncement /></>);
    server.afterNext({ fault: { kind: "transient" }, intent: "pause", status: "paused", stream: null, event: { seq: 99, kind: "transient_failure" } });
    act(() => { engine.playTracks([playerTrack("1")]); });
    expect(await screen.findByText("读取失败", { exact: true })).toBeTruthy();
    expect(screen.getByRole("status").textContent).toBe("暂时无法读取音频，已暂停，可以重试。");
    await userEvent.click(screen.getByRole("button", { name: "重试播放" }));
    await waitFor(() => expect(engine.getSnapshot().fault).toBeNull());
    expect(server.commands.at(-1)).toEqual({ command: "retry" });
    expect(screen.queryByRole("button", { name: "重试播放" })).toBeNull();
  });

  it("命令因设备离线被拒时说一句，下一条命令成功后消失", async () => {
    await enqueue([playerTrack("1")]);
    view(<><PlayerBar /><PlayerAnnouncement /></>);
    server.failNext(409, "zone_offline");
    act(() => { engine.play(); });
    await waitFor(() => expect(screen.getAllByText("那台设备已离线。")).toHaveLength(2));
    expect(screen.getByRole("status").textContent).toContain("那台设备已离线。");
    act(() => { engine.pause(); });
    await waitFor(() => expect(screen.queryAllByText("那台设备已离线。")).toHaveLength(0));
  });

  it("自动播放被拒绝时保持暂停并给出恢复动作", async () => {
    await waitFor(() => expect(audios).toHaveLength(2));
    for (const audio of audios) audio.playBehavior = "not-allowed";
    await enqueue([playerTrack("1")]);
    view(<><PlayerBar /><PlayerAnnouncement /></>);
    act(() => { engine.play(); });
    expect(await screen.findByText("请按播放")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "暂停" })).toBeNull();
    expect(screen.getByRole("status").textContent).toBe("浏览器阻止了自动播放，请按播放。");
  });

  it("缓冲状态保持暂停动作", async () => {
    await enqueue([playerTrack("1")]);
    view(<PlayerBar />);
    await userEvent.click(screen.getByRole("button", { name: "播放" }));
    const audio = await sounding();
    act(() => audio.stall());
    expect(await screen.findByText("正在缓冲")).toBeTruthy();
    expect(screen.getByRole("button", { name: "暂停" })).toBeTruthy();
  });

  it("要播放时按钮写“暂停”，按下发出暂停", async () => {
    view(<PlayerBar />);
    act(() => { engine.playTracks([playerTrack("1")]); });
    await userEvent.click(await screen.findByRole("button", { name: "暂停" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "播放" })).toBeTruthy());
    expect(server.commands.at(-1)).toEqual({ command: "pause" });
  });

  it("失败的曲目在队列里写明原因", async () => {
    await enqueue([playerTrack("1")]);
    view(<PlayerQueue headingId="queue-heading" onClose={() => undefined} />);
    const entry = engine.getSnapshot().queue.entries[0];
    if (entry === undefined) throw new Error("队列缺少待播放曲目");
    server.afterNext({ failures: { [entry.entryId]: "decode_failed" } });
    act(() => { engine.playEntry(entry.entryId); });
    expect(await screen.findByText("这首曲目解码失败")).toBeTruthy();
  });

  it("暂时无法读取与自动播放拒绝进入常驻全局公告", async () => {
    view(<PlayerAnnouncement />);
    const region = screen.getByRole("status");
    server.afterNext({ fault: { kind: "transient" }, intent: "pause", status: "paused", stream: null, event: { seq: 99, kind: "transient_failure" } });
    act(() => { engine.playTracks([playerTrack("1")]); });
    await waitFor(() => expect(region.textContent).toBe("暂时无法读取音频，已暂停，可以重试。"));
    for (const audio of audios) audio.playBehavior = "not-allowed";
    act(() => { engine.retry(); });
    await waitFor(() => expect(region.textContent).toBe("浏览器阻止了自动播放，请按播放。"));
    expect(screen.getByRole("status")).toBe(region);
  });

  it("播放模式循环、静音、音量与下一首可用", async () => {
    await enqueue([playerTrack("1"), playerTrack("2")]);
    view(<PlayerBar />);
    for (const [current, next, mode] of [
      ["顺序", "列表循环", "repeat_all"], ["列表循环", "单曲循环", "repeat_one"], ["单曲循环", "随机", "shuffle"], ["随机", "顺序", "sequential"],
    ]) {
      await userEvent.click(screen.getByRole("button", { name: `播放模式：${current}` }));
      await waitFor(() => expect(engine.getSnapshot().queue.mode).toBe(mode));
      expect(screen.getByRole("button", { name: `播放模式：${next}` })).toBeTruthy();
    }
    await userEvent.click(screen.getByRole("button", { name: "静音" }));
    expect(engine.getSnapshot().muted).toBe(true);
    fireEvent.change(screen.getByRole("slider", { name: "播放器音量" }), { target: { value: "0.4" } });
    expect(engine.getSnapshot().volume).toBe(0.4);
    await userEvent.click(screen.getByRole("button", { name: "下一首" }));
    await waitFor(() => expect(engine.getSnapshot().current?.track.trackId).toBe("track-2"));
  });

  it("选曲公告不把加载意图说成已经播放", async () => {
    view(<PlayerAnnouncement />);
    act(() => { engine.playTracks([playerTrack("未加载")]); });
    await waitFor(() => expect(screen.getByRole("status").textContent).toBe("已选择曲目：曲目 未加载"));
    expect(screen.getByRole("status").textContent).not.toContain("正在播放");
    expect(engine.getSnapshot().status).toBe("loading");
  });

  it("队列重复曲目按 entryId 上移、移除；清空必须确认", async () => {
    const user = userEvent.setup();
    await enqueue([playerTrack("1"), playerTrack("1"), playerTrack("2")]);
    const secondId = engine.getSnapshot().queue.entries[1]?.entryId;
    view(<PlayerQueue headingId="queue-heading" onClose={() => undefined} />);
    let rows = screen.getAllByRole("listitem");
    const secondRow = rows[1];
    if (secondRow === undefined) throw new Error("缺少第二项");
    await user.click(within(secondRow).getByRole("button", { name: "上移" }));
    await waitFor(() => expect(engine.getSnapshot().queue.entries[0]?.entryId).toBe(secondId));
    rows = screen.getAllByRole("listitem");
    const firstRow = rows[0];
    if (firstRow === undefined) throw new Error("缺少第一项");
    await user.click(within(firstRow).getByRole("button", { name: "移除" }));
    await waitFor(() => expect(engine.getSnapshot().queue.entries).toHaveLength(2));
    expect(engine.getSnapshot().queue.entries.some((entry) => entry.entryId === secondId)).toBe(false);
    await user.click(screen.getByRole("button", { name: "清空队列" }));
    const dialog = screen.getByRole("dialog", { name: "清空播放队列？" });
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(engine.getSnapshot().queue.entries).toHaveLength(2);
    await user.click(screen.getByRole("button", { name: "清空队列" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "清空队列" }));
    await waitFor(() => expect(engine.getSnapshot().queue.entries).toHaveLength(0));
  });

  it("播放栏长标题保留完整名称；未知时长禁止拖动，进度来自出声的流", async () => {
    const longTitle = "长标题".repeat(400);
    await enqueue([playerTrack("1", { title: longTitle, durationSeconds: null })]);
    view(<PlayerBar />);
    expect(screen.getByText(longTitle, { exact: true })).toBeTruthy();
    const seek = screen.getByRole("slider", { name: "播放进度" });
    expect((seek as HTMLInputElement).disabled).toBe(true);
    expect(seek.getAttribute("aria-valuetext")).toBe("0:00 / —:—");
    act(() => { engine.playTracks([playerTrack("2", { durationSeconds: 120 })]); });
    const audio = await sounding();
    act(() => audio.tick(83));
    await waitFor(() => expect(screen.getByRole("slider", { name: "播放进度" }).getAttribute("aria-valuetext")).toBe("1:23 / 2:00"));
    expect(screen.getByRole("button", { name: "暂停" })).toBeTruthy();
    expect((screen.getByRole("slider", { name: "播放进度" }) as HTMLInputElement).disabled).toBe(false);
    fireEvent.change(screen.getByRole("slider", { name: "播放进度" }), { target: { value: "90" } });
    await waitFor(() => expect(server.commands.at(-1)).toEqual({ command: "seek", position_seconds: 90 }));
  });
});

describe("专辑播放入口", () => {
  function release() {
    const detail = releaseDetailOf(albumGroup(1, { track_count: 3 }).representative);
    const medium = detail.media[0];
    if (medium === undefined || medium.tracks[0] === undefined) throw new Error("缺少测试曲目");
    const first = medium.tracks[0];
    return { ...detail, media: [{ ...medium, tracks: [{ ...first, id: "track-1", codec: "flac", source_kind: "flac_vorbis_comment" }, { ...first, id: "track-2", codec: "alac", source_kind: "m4a" }, { ...first, id: "track-3", codec: "flac", source_kind: "cue_virtual" }] }] };
  }
  it("整张立即播放处理队列容量拒绝，不谎报成功", async () => {
    vi.spyOn(engine, "playTracks").mockReturnValue("queue_full");
    view(<AlbumPlaybackActions release={release()} groupId="group-1" />);
    await userEvent.click(screen.getByRole("button", { name: "播放整张专辑" }));
    expect(screen.getByRole("status").textContent).toContain("队列已满，请先移除一些条目。");
    expect(server.commands).toEqual([]);
  });
  it("一首都不能播放时禁用整张动作并在头部解释", () => {
    table = { verdictOf: () => ({ playable: false, reason: "format_unsupported" }) };
    view(<AlbumPlaybackActions release={release()} groupId="group-1" />);
    expect((screen.getByRole("button", { name: "播放整张专辑" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/这个版本没有能播放的曲目/)).toBeTruthy();
  });
  it("整张播放跳过不可播放项（CUE 分轨照常播放），加入队列不打断当前项", async () => {
    view(<AlbumPlaybackActions release={release()} groupId="group-1" />);
    await userEvent.click(screen.getByRole("button", { name: "播放整张专辑" }));
    await waitFor(() => expect(engine.getSnapshot().queue.entries.map((entry) => entry.track.trackId)).toEqual(["track-1", "track-3"]));
    expect(screen.getByRole("status").textContent).toContain("跳过 1 首");
    const currentId = engine.getSnapshot().current?.entryId;
    await userEvent.click(screen.getByRole("button", { name: "加入队列" }));
    await waitFor(() => expect(engine.getSnapshot().queue.entries).toHaveLength(4));
    expect(engine.getSnapshot().current?.entryId).toBe(currentId);
  });
});
