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
import { MAX_QUEUE_ENTRIES, type PlayerTrack } from "../engine/queue";
import type { CapabilityTable } from "../engine/capability";
import { MenuButton } from "../../../shared/ui";
import type { ReleaseDetailDTO } from "../../catalog";
import { useAlbumPlayback } from "./album-playback-actions";
import { FullscreenPlayer } from "./fullscreen-player";
import { PlayerBar } from "./player-bar";
import barStyles from "./player-bar.module.css";
import { PlayerQueue } from "./player-queue";
import { PlayerAnnouncement } from "./player-feedback";
import { SignalPathButton } from "./signal-path";
import { WaveformSeek } from "./waveform-seek";
import { PlaybackSettingsDevice, PlaybackSettingsForm } from "./playback-settings-form";
import { decodeTrackAnalysis, decodeTrackLyrics } from "../api/decoders";
import { sampleBody } from "../../../test/api-samples";

const fullscreenState = vi.hoisted(() => ({ open: false, listeners: new Set<() => void>() }));
const analysisState = vi.hoisted(() => ({ value: null as unknown }));
const lyricsState = vi.hoisted(() => ({ value: null as unknown }));

vi.mock("@tanstack/react-router", () => ({
  Link: ({ to, children, hash, title }: { to: string; children: ReactNode; hash?: string; title?: string }) => <a href={`${to}${hash === undefined ? "" : `#${hash}`}`} title={title}>{children}</a>,
  useRouterState: ({ select }: { select: (state: { location: { pathname: string } }) => unknown }) => select({ location: { pathname: "/" } }),
  useNavigate: () => () => Promise.resolve(),
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
  useTrackLyrics: function useTestLyrics() { return { status: "success", data: lyricsState.value, error: null, refetch: () => undefined }; },
}));
vi.mock("../player-provider", () => ({
  usePlayer: function useTestPlayer() { return { engine, snapshot: useSyncExternalStore(engine.subscribe, engine.getSnapshot) }; },
  usePlayerEngine: function useTestPlayerEngine() { return engine; },
  usePlayerSelector: function useTestPlayerSelector<T>(select: (snapshot: PlayerSnapshot) => T) { return useSyncExternalStore(engine.subscribe, () => select(engine.getSnapshot())); },
  usePlayerProgress: function useTestProgress() { return useSyncExternalStore(engine.subscribeProgress, engine.getProgress); },
  usePlayerProgressSelector: function useTestProgressSelector<T>(select: (progress: PlayerProgress) => T) { return useSyncExternalStore(engine.subscribeProgress, () => select(engine.getProgress())); },
}));
vi.mock("../../auth", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../auth")>(),
  useViewerScope: () => ({ generation: 1 }),
}));
vi.mock("../convolution/hooks", async (importOriginal) => ({
  ...await importOriginal<typeof import("../convolution/hooks")>(),
  useIrFiles: () => ({ data: [] }),
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
  lyricsState.value = null;
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

  it("全屏没有歌词时右栏默认是“接下来”：列出当前之后的条目，点一条播放它", async () => {
    const user = userEvent.setup();
    await enqueue([playerTrack("1"), playerTrack("2"), playerTrack("3")]);
    act(() => { engine.playTracks(engine.getSnapshot().queue.entries.map((entry) => entry.track)); });
    await waitFor(() => expect(engine.getSnapshot().current?.track.trackId).toBe("track-1"));
    fullscreenState.open = true;
    view(<FullscreenPlayer />);
    const dialog = await screen.findByRole("dialog", { name: "曲目 1" });
    expect(within(dialog).getByRole("button", { name: "接下来" }).getAttribute("aria-pressed")).toBe("true");
    expect(within(dialog).getByRole("button", { name: "歌词" }).getAttribute("aria-pressed")).toBe("false");
    const upNext = within(dialog).getByRole("region", { name: "接下来" });
    expect(within(upNext).getAllByRole("button").map((item) => item.getAttribute("aria-label"))).toEqual(["播放《曲目 2》", "播放《曲目 3》"]);
    expect(within(upNext).queryByText("随机播放中，顺序以实际播放为准")).toBeNull();
    const third = engine.getSnapshot().queue.entries[2]?.entryId;
    await user.click(within(upNext).getByRole("button", { name: "播放《曲目 3》" }));
    await waitFor(() => expect(engine.getSnapshot().current?.entryId).toBe(third));
    await waitFor(() => expect(within(upNext).getByText("队列里没有接下来的曲目")).toBeTruthy());
  });

  it("全屏“接下来”在随机模式下写明顺序以实际播放为准", async () => {
    await enqueue([playerTrack("1"), playerTrack("2")]);
    act(() => { engine.setMode("shuffle"); });
    await waitFor(() => expect(engine.getSnapshot().queue.mode).toBe("shuffle"));
    fullscreenState.open = true;
    view(<FullscreenPlayer />);
    const dialog = await screen.findByRole("dialog", { name: "曲目 1" });
    expect(within(dialog).getByText("随机播放中，顺序以实际播放为准")).toBeTruthy();
    expect(within(dialog).getByRole("button", { name: "播放模式：随机" }).getAttribute("aria-pressed")).toBe("true");
  });

  it("全屏有歌词时右栏默认是歌词，可以切到“接下来”再切回", async () => {
    const user = userEvent.setup();
    lyricsState.value = decodeTrackLyrics(sampleBody("tracks/lyrics-plain")).lyrics;
    await enqueue([playerTrack("1"), playerTrack("2")]);
    fullscreenState.open = true;
    view(<FullscreenPlayer />);
    const dialog = await screen.findByRole("dialog", { name: "曲目 1" });
    expect(within(dialog).getByRole("button", { name: "歌词" }).getAttribute("aria-pressed")).toBe("true");
    expect(within(dialog).getByRole("group", { name: "歌词文本" })).toBeTruthy();
    expect(within(dialog).queryByRole("region", { name: "接下来" })).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "接下来" }));
    expect(within(dialog).getByRole("region", { name: "接下来" })).toBeTruthy();
    expect(within(dialog).queryByRole("group", { name: "歌词文本" })).toBeNull();
    await user.click(within(dialog).getByRole("button", { name: "歌词" }));
    expect(within(dialog).getByRole("group", { name: "歌词文本" })).toBeTruthy();
  });

  it("全屏宽屏：顶栏只有收起，标题旁有“更多操作”，设备、传输键、模式与音量在控制台", async () => {
    await enqueue([playerTrack("1")]);
    fullscreenState.open = true;
    view(<FullscreenPlayer />);
    const dialog = await screen.findByRole("dialog", { name: "曲目 1" });
    const collapse = within(dialog).getByRole("button", { name: "收起全屏播放器" });
    const device = within(dialog).getByRole("button", { name: "播放设备" });
    const heading = within(dialog).getByRole("heading", { name: "曲目 1", level: 2 });
    // 队列从右栏的“接下来”看，顶栏不再有队列按钮。
    expect(within(dialog).queryByRole("button", { name: /^队列/ })).toBeNull();
    const topbar = collapse.parentElement?.parentElement;
    expect(topbar?.querySelectorAll("button")).toHaveLength(1);
    expect(topbar?.contains(device)).toBe(false);
    expect(heading.compareDocumentPosition(device) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const titleRow = heading.parentElement;
    if (titleRow === null) throw new Error("缺少标题行");
    expect(within(titleRow).getByRole("button", { name: "更多操作" })).toBeTruthy();
    // 标题行不再放信号路径，空信号格位于控制台。
    expect(within(titleRow).queryByRole("button", { name: /^信号路径：/ })).toBeNull();
    expect(titleRow.textContent).toContain("曲目 1");
    for (const control of [
      within(dialog).getByRole("slider", { name: "播放进度" }),
      device,
      within(dialog).getByRole("button", { name: "播放模式：顺序" }),
      within(dialog).getByRole("button", { name: "音量" }),
    ]) expect(heading.compareDocumentPosition(control) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("全屏信号路径在控件行最前面，随后是设备、传输键、模式与音量", async () => {
    const event = decodeStreamEvent(signalEvent.event, JSON.stringify(signalEvent.data));
    if (event?.type !== "zone") throw new Error("信号样本未解码为播放区事件");
    server.afterNext({ signal: event.zone.signal });
    act(() => { engine.playTracks([playerTrack("1")]); });
    await waitFor(() => expect(engine.getSnapshot().zone?.signal).toEqual(event.zone.signal));
    fullscreenState.open = true;
    view(<FullscreenPlayer />);
    const dialog = await screen.findByRole("dialog", { name: "曲目 1" });
    const heading = within(dialog).getByRole("heading", { name: "曲目 1", level: 2 });
    const titleRow = heading.parentElement;
    if (titleRow === null) throw new Error("缺少标题行");
    expect(within(titleRow).queryByRole("button", { name: /^信号路径：/ })).toBeNull();
    const signal = within(dialog).getByRole("button", { name: /^信号路径：.+，增强，削波$/ });
    expect(signal.textContent).toBe("");
    const controls = signal.parentElement?.parentElement?.parentElement;
    if (controls === null || controls === undefined) throw new Error("缺少传输行");
    expect(within(controls).getAllByRole("button").map((button) => button.getAttribute("aria-label"))).toEqual([
      signal.getAttribute("aria-label"), "播放设备", "上一首", "暂停", "下一首", "播放模式：顺序", "音量",
    ]);
  });

  it("播放栏：信号路径在传输行最前面；右侧依次是模式、队列、分隔线、设备、音量", async () => {
    const event = decodeStreamEvent(signalEvent.event, JSON.stringify(signalEvent.data));
    if (event?.type !== "zone") throw new Error("信号样本未解码为播放区事件");
    server.afterNext({ signal: event.zone.signal });
    act(() => { engine.playTracks([playerTrack("1")]); });
    await waitFor(() => expect(engine.getSnapshot().zone?.signal).toEqual(event.zone.signal));
    view(<PlayerBar />);
    const bar = screen.getByRole("region", { name: "播放器" });
    const title = within(bar).getByText("曲目 1", { selector: "p" });
    const signal = within(bar).getByRole("button", { name: /^信号路径：/ });
    expect(title.parentElement?.contains(signal)).toBe(false);
    const controls = signal.parentElement?.parentElement?.parentElement;
    if (controls === null || controls === undefined) throw new Error("缺少传输行");
    expect(within(controls).getAllByRole("button").map((button) => button.getAttribute("aria-label"))).toEqual([
      signal.getAttribute("aria-label"), "上一首", "暂停", "下一首",
    ]);
    expect(title.compareDocumentPosition(signal) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const queue = within(bar).getByRole("button", { name: "队列（1）" });
    const device = within(bar).getByRole("button", { name: "播放设备" });
    const volume = within(bar).getByRole("button", { name: "音量" });
    const mode = within(bar).getByRole("button", { name: "播放模式：顺序" });
    expect(mode.compareDocumentPosition(queue) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(device.textContent).toBe("");
    expect(queue.compareDocumentPosition(device) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(device.compareDocumentPosition(volume) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // 分隔线在队列之后、设备之前。
    const separator = bar.querySelector(`.${barStyles.separator}`);
    if (separator === null) throw new Error("缺少分隔线");
    expect(queue.compareDocumentPosition(separator) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(separator.compareDocumentPosition(device) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // 本机时设备按钮所在的位置没有远端标记（迷你栏据此隐藏它）。
    expect(device.closest("[data-remote]")).toBeNull();
  });

  it("全屏窄屏：底部有“歌词”按钮，切到歌词视图后进度与传输键还是同一组元素", async () => {
    const user = userEvent.setup();
    lyricsState.value = decodeTrackLyrics(sampleBody("tracks/lyrics-plain")).lyrics;
    const original = window.matchMedia;
    window.matchMedia = (query: string) => ({ ...original(query), matches: query === "(max-width: 767px)" });
    try {
      await enqueue([playerTrack("1")]);
      fullscreenState.open = true;
      view(<FullscreenPlayer />);
      const dialog = await screen.findByRole("dialog", { name: "曲目 1" });
      expect(within(dialog).getByText("正在播放")).toBeTruthy();
      const lyricsButton = within(dialog).getByRole("button", { name: "歌词" });
      expect(lyricsButton.getAttribute("aria-pressed")).toBe("false");
      expect(within(dialog).queryByRole("group", { name: "歌词文本" })).toBeNull();
      const seek = within(dialog).getByRole("slider", { name: "播放进度" });
      const play = within(dialog).getByRole("button", { name: "播放" });
      await user.click(lyricsButton);
      expect(within(dialog).getByRole("button", { name: "歌词" }).getAttribute("aria-pressed")).toBe("true");
      expect(within(dialog).getByRole("group", { name: "歌词文本" })).toBeTruthy();
      expect(within(dialog).getByRole("heading", { name: "曲目 1", level: 2 })).toBeTruthy();
      // 控制区不随视图重建：仍是同一个进度条与播放键。
      expect(within(dialog).getByRole("slider", { name: "播放进度" })).toBe(seek);
      expect(within(dialog).getByRole("button", { name: "播放" })).toBe(play);
      await user.click(within(dialog).getByRole("button", { name: "歌词" }));
      expect(within(dialog).queryByRole("group", { name: "歌词文本" })).toBeNull();
      // 点唱片也能切到歌词（旧交互保留）。
      await user.click(within(dialog).getByRole("button", { name: "显示歌词" }));
      expect(within(dialog).getByRole("group", { name: "歌词文本" })).toBeTruthy();
    } finally {
      window.matchMedia = original;
    }
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

  it("从属设置随所属项出现与消失：交叉渐变下的渐变时长、指定采样率下的目标采样率", async () => {
    const user = userEvent.setup();
    await waitFor(() => expect(engine.getSnapshot().zone).not.toBeNull());
    view(<PlaybackSettingsForm />);
    expect(screen.getByRole("region", { name: "播放" })).toBeTruthy();
    expect(screen.getByRole("region", { name: "输出" })).toBeTruthy();
    expect(screen.getByRole("region", { name: "音效处理" })).toBeTruthy();
    expect(screen.queryByRole("radiogroup", { name: "渐变时长" })).toBeNull();
    await user.click(screen.getByRole("switch", { name: "交叉渐变" }));
    expect(await screen.findByRole("radiogroup", { name: "渐变时长" })).toBeTruthy();
    await user.click(screen.getByRole("switch", { name: "交叉渐变" }));
    await waitFor(() => expect(screen.queryByRole("radiogroup", { name: "渐变时长" })).toBeNull());

    expect(screen.queryByRole("combobox", { name: "目标采样率" })).toBeNull();
    await user.selectOptions(screen.getByRole("combobox", { name: "采样率转换" }), "指定采样率");
    expect(await screen.findByRole("combobox", { name: "目标采样率" })).toBeTruthy();
    await user.selectOptions(screen.getByRole("combobox", { name: "采样率转换" }), "不转换");
    await waitFor(() => expect(screen.queryByRole("combobox", { name: "目标采样率" })).toBeNull());
  });

  it("设置页页首写出设置作用于正在控制的设备", async () => {
    await waitFor(() => expect(engine.getSnapshot().device).not.toBeNull());
    const name = engine.getSnapshot().device?.name ?? "";
    expect(name).not.toBe("");
    const { container } = render(<PlaybackSettingsDevice />);
    expect(container.textContent).toBe(`这些设置作用于正在控制的设备：${name}`);
  });

  it("Headroom 与削波开关各发送一次完整设置，保留其他 Headroom 字段", async () => {
    const user = userEvent.setup();
    await waitFor(() => expect(engine.getSnapshot().zone).not.toBeNull());
    view(<PlaybackSettingsForm />);
    // 增益是 Headroom 的从属项：Headroom 关着时整行不出现。
    expect(screen.queryByRole("slider", { name: "增益" })).toBeNull();
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

  it("DownmixSwitchSubmitsDisabledSettings", async () => {
    const user = userEvent.setup();
    await waitFor(() => expect(engine.getSnapshot().zone).not.toBeNull());
    view(<PlaybackSettingsForm />);
    const downmix = screen.getByRole("switch", { name: "多声道降为双声道" });
    expect((downmix as HTMLInputElement).checked).toBe(true);
    await user.click(downmix);
    await waitFor(() => expect(engine.getSnapshot().settings.downmix.enabled).toBe(false));
    expect((downmix as HTMLInputElement).checked).toBe(false);
    expect(server.commands).toEqual([
      { command: "set_settings", settings: { downmix: { enabled: false } } },
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
    const volume = screen.getByRole("button", { name: "音量" });
    expect((volume as HTMLButtonElement).disabled).toBe(true);
    expect(volume.getAttribute("aria-haspopup")).toBe("dialog");
    expect(volume.closest("[title]")?.getAttribute("title")).toBe("这台设备不能调音量");
    expect(screen.queryByRole("slider", { name: "播放器音量" })).toBeNull();
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
    await user.click(screen.getByRole("button", { name: "信号路径：ALAC 24/48，增强，削波" }));
    const dialog = await screen.findByRole("dialog", { name: "信号路径：增强，削波" });
    expect(within(dialog).getAllByRole("listitem").map((row) => row.textContent)).toEqual([
      "来源WAV 48 kHz 16 bit",
      "Headroom−3.1 dB",
      "抖动TPDF 16 bit",
      "编码WAV 48 kHz 16 bit",
      "输出测试设备",
    ]);
    expect([...dialog.querySelectorAll("dl > div")].map((cell) => cell.textContent)).toEqual(["处理速度85.3×"]);
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

  it("播放模式依次为顺序、列表循环、单曲循环、随机，再回到顺序", async () => {
    await enqueue([playerTrack("1"), playerTrack("2")]);
    view(<PlayerBar />);
    expect(screen.getByRole("button", { name: "播放模式：顺序" }).getAttribute("aria-pressed")).toBe("false");
    for (const [current, next, mode] of [
      ["顺序", "列表循环", "repeat_all"], ["列表循环", "单曲循环", "repeat_one"],
      ["单曲循环", "随机", "shuffle"], ["随机", "顺序", "sequential"],
    ] as const) {
      await userEvent.click(screen.getByRole("button", { name: `播放模式：${current}` }));
      await waitFor(() => expect(engine.getSnapshot().queue.mode).toBe(mode));
      expect(screen.getByRole("button", { name: `播放模式：${next}` }).getAttribute("aria-pressed")).toBe(mode === "sequential" ? "false" : "true");
      expect(screen.getAllByRole("button", { name: /^播放模式：/ })).toHaveLength(1);
    }
  });

  it("音量按钮打开弹层后可静音、取消静音与调节音量，关闭后下一首仍可用", async () => {
    await enqueue([playerTrack("1"), playerTrack("2")]);
    view(<PlayerBar />);
    const volume = screen.getByRole("button", { name: "音量" });
    expect(volume.getAttribute("aria-haspopup")).toBe("dialog");
    expect(screen.queryByRole("slider", { name: "播放器音量" })).toBeNull();
    await userEvent.click(volume);
    const dialog = screen.getByRole("dialog", { name: /^音量/ });
    const slider = within(dialog).getByRole("slider", { name: "播放器音量" });
    expect(document.activeElement).toBe(slider);
    await userEvent.click(within(dialog).getByRole("button", { name: "静音" }));
    expect(engine.getSnapshot().muted).toBe(true);
    expect(volume.getAttribute("aria-label")).toBe("音量（已静音）");
    await userEvent.click(within(dialog).getByRole("button", { name: "取消静音" }));
    expect(engine.getSnapshot().muted).toBe(false);
    fireEvent.change(slider, { target: { value: "0.4" } });
    expect(engine.getSnapshot().volume).toBe(0.4);
    expect(slider.getAttribute("aria-valuetext")).toBe("40%");
    fireEvent(dialog, new Event("cancel", { bubbles: true, cancelable: true }));
    await waitFor(() => expect(dialog.hasAttribute("open")).toBe(false));
    expect(document.activeElement).toBe(volume);
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

/** 与专辑页相同的组合：圆形播放、随机播放、“更多操作”（下一首播放、加入队列），下面是状态。 */
function AlbumPlayback({ release, groupId }: { release: ReleaseDetailDTO; groupId: string }) {
  const playback = useAlbumPlayback({ release, groupId });
  return <>{playback.play}{playback.shuffle}<MenuButton label="更多操作" iconOnly icon={<span />} actions={playback.actions} />{playback.status}</>;
}

describe("专辑播放入口", () => {
  async function chooseMore(name: string) {
    await userEvent.click(screen.getByRole("button", { name: "更多操作" }));
    await userEvent.click(screen.getByRole("menuitem", { name }));
  }
  function release() {
    const detail = releaseDetailOf(albumGroup(1, { track_count: 3 }).representative);
    const medium = detail.media[0];
    if (medium === undefined || medium.tracks[0] === undefined) throw new Error("缺少测试曲目");
    const first = medium.tracks[0];
    return { ...detail, media: [{ ...medium, tracks: [{ ...first, id: "track-1", codec: "flac", source_kind: "flac_vorbis_comment" }, { ...first, id: "track-2", codec: "alac", source_kind: "m4a" }, { ...first, id: "track-3", codec: "flac", source_kind: "cue_virtual" }] }] };
  }
  it("整张立即播放处理队列容量拒绝，不谎报成功", async () => {
    vi.spyOn(engine, "playTracks").mockReturnValue("queue_full");
    view(<AlbumPlayback release={release()} groupId="group-1" />);
    await userEvent.click(screen.getByRole("button", { name: "播放整张专辑" }));
    expect(screen.getByRole("status").textContent).toContain("队列已满，请先移除一些条目。");
    expect(server.commands).toEqual([]);
  });
  it("一首都不能播放时禁用整张动作并在头部解释", () => {
    table = { verdictOf: () => ({ playable: false, reason: "format_unsupported" }) };
    view(<AlbumPlayback release={release()} groupId="group-1" />);
    expect((screen.getByRole("button", { name: "播放整张专辑" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "随机播放" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/这个版本没有能播放的曲目/)).toBeTruthy();
  });
  it("整张播放跳过不可播放项（CUE 分轨照常播放），加入队列不打断当前项", async () => {
    view(<AlbumPlayback release={release()} groupId="group-1" />);
    await userEvent.click(screen.getByRole("button", { name: "播放整张专辑" }));
    await waitFor(() => expect(engine.getSnapshot().queue.entries.map((entry) => entry.track.trackId)).toEqual(["track-1", "track-3"]));
    expect(screen.getByRole("status").textContent).toContain("跳过 1 首");
    const currentId = engine.getSnapshot().current?.entryId;
    await chooseMore("加入队列");
    await waitFor(() => expect(engine.getSnapshot().queue.entries).toHaveLength(4));
    expect(engine.getSnapshot().current?.entryId).toBe(currentId);
    expect(screen.getByRole("status").textContent).toBe("已加入队列 2 首，跳过 1 首不可播放的曲目。");
  });
  it("随机播放：切到随机模式，放整张能播放的曲目（跳过不可播放的），从随机的一首开始", async () => {
    vi.spyOn(Math, "random").mockReturnValue(0.99);
    view(<AlbumPlayback release={release()} groupId="group-1" />);
    await userEvent.click(screen.getByRole("button", { name: "随机播放" }));
    await waitFor(() => expect(engine.getSnapshot().queue.mode).toBe("shuffle"));
    await waitFor(() => expect(engine.getSnapshot().queue.entries.map((entry) => entry.track.trackId).sort()).toEqual(["track-1", "track-3"]));
    // Math.random 给 0.99：起点是能播放的最后一首。
    await waitFor(() => expect(engine.getSnapshot().current?.track.trackId).toBe("track-3"));
    expect(screen.getByRole("status").textContent).toBe("已随机播放 2 首，跳过 1 首不可播放的曲目。");
    vi.mocked(Math.random).mockRestore();
  });
  it("随机播放在队列放不下时不切模式，说明队列已满", async () => {
    vi.spyOn(engine, "playTracks").mockReturnValue("queue_full");
    const many = release();
    const medium = many.media[0];
    const first = medium?.tracks[0];
    if (medium === undefined || first === undefined) throw new Error("缺少测试曲目");
    const tracks = Array.from({ length: MAX_QUEUE_ENTRIES + 1 }, (_, index) => ({ ...first, id: `track-${index}` }));
    view(<AlbumPlayback release={{ ...many, media: [{ ...medium, tracks }] }} groupId="group-1" />);
    await userEvent.click(screen.getByRole("button", { name: "随机播放" }));
    expect(screen.getByRole("status").textContent).toBe("队列已满，请先移除一些条目。");
    expect(engine.getSnapshot().queue.mode).toBe("sequential");
    expect(engine.playTracks).not.toHaveBeenCalled();
  });
  it("正在放别的专辑：大按钮仍是“播放整张专辑”，按下换成这张专辑从第一首放起", async () => {
    await enqueue([playerTrack("别的", { releaseId: "release-other", groupId: "group-other" })]);
    act(() => { engine.play(); });
    await waitFor(() => expect(engine.getSnapshot().zone?.intent).toBe("play"));
    view(<AlbumPlayback release={release()} groupId="group-1" />);
    expect(screen.queryByRole("button", { name: "暂停播放" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "播放整张专辑" }));
    await waitFor(() => expect(engine.getSnapshot().current?.track.trackId).toBe("track-1"));
  });
  it("正在放这张专辑：大按钮是“暂停播放”，按下暂停而不是从第一首重放；暂停后是“继续播放”，按下从原处继续", async () => {
    view(<AlbumPlayback release={release()} groupId="group-1" />);
    await userEvent.click(screen.getByRole("button", { name: "播放整张专辑" }));
    await waitFor(() => expect(engine.getSnapshot().current?.track.trackId).toBe("track-1"));
    // 已经在放第二首（CUE 分轨）：暂停与继续都不能回到第一首。
    act(() => { engine.next(); });
    await waitFor(() => expect(engine.getSnapshot().current?.track.trackId).toBe("track-3"));
    const entries = engine.getSnapshot().queue.entries;
    const pause = await screen.findByRole("button", { name: "暂停播放" });
    expect(screen.queryByRole("button", { name: "播放整张专辑" })).toBeNull();
    await userEvent.click(pause);
    await waitFor(() => expect(engine.getSnapshot().zone?.intent).toBe("pause"));
    expect(server.commands.at(-1)).toMatchObject({ command: "pause" });
    expect(engine.getSnapshot().current?.track.trackId).toBe("track-3");
    await userEvent.click(await screen.findByRole("button", { name: "继续播放" }));
    await waitFor(() => expect(engine.getSnapshot().zone?.intent).toBe("play"));
    expect(server.commands.at(-1)).toMatchObject({ command: "play" });
    expect(engine.getSnapshot().current?.track.trackId).toBe("track-3");
    expect(engine.getSnapshot().queue.entries).toBe(entries);
    expect(await screen.findByRole("button", { name: "暂停播放" })).toBeTruthy();
  });
  it("同一个专辑组的其他版本正在播放也算这张专辑：不能播放的版本也给出可用的暂停", async () => {
    table = { verdictOf: () => ({ playable: false, reason: "format_unsupported" }) };
    await enqueue([playerTrack("别的版本", { releaseId: "release-other", groupId: "group-1" })]);
    act(() => { engine.play(); });
    await waitFor(() => expect(engine.getSnapshot().zone?.intent).toBe("play"));
    view(<AlbumPlayback release={release()} groupId="group-1" />);
    const pause = await screen.findByRole("button", { name: "暂停播放" });
    expect((pause as HTMLButtonElement).disabled).toBe(false);
    await userEvent.click(pause);
    await waitFor(() => expect(engine.getSnapshot().zone?.intent).toBe("pause"));
  });
  it("下一首播放：整张能播放的曲目插到当前曲目之后，不打断当前曲目", async () => {
    await enqueue([playerTrack("当前"), playerTrack("原来的下一首")]);
    act(() => { engine.playTracks(engine.getSnapshot().queue.entries.map((entry) => entry.track)); });
    await waitFor(() => expect(engine.getSnapshot().current?.track.trackId).toBe("track-当前"));
    const currentId = engine.getSnapshot().current?.entryId;
    view(<AlbumPlayback release={release()} groupId="group-1" />);
    await chooseMore("下一首播放");
    await waitFor(() => expect(engine.getSnapshot().queue.entries.map((entry) => entry.track.trackId)).toEqual(["track-当前", "track-1", "track-3", "track-原来的下一首"]));
    expect(engine.getSnapshot().current?.entryId).toBe(currentId);
    expect(screen.getByRole("status").textContent).toBe("已设为下一首播放 2 首，跳过 1 首不可播放的曲目。");
  });
});
