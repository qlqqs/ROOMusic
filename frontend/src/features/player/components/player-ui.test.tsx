import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useSyncExternalStore, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { albumGroup, releaseDetailOf } from "../../../test/catalog-data";
import { FakeAudio } from "../../../test/fake-audio";
import { playerTrack } from "../../../test/player-data";
import { memoryStorage } from "../../../test/memory-storage";
import { silentPlayRecorder } from "../../../test/play-recorder";
import { ApiError } from "../../../shared/api";
import { PlayerEngine, type EngineDeps, type PlayerProgress, type PlayerSnapshot } from "../engine/player-engine";
import type { CapabilityTable } from "../engine/capability";
import type { TrackPlaybackResult } from "../api/endpoints";
import { AlbumPlaybackActions } from "./album-playback-actions";
import { FullscreenPlayer } from "./fullscreen-player";
import { PlayerBar } from "./player-bar";
import { PlayerQueue } from "./player-queue";
import { PlayerAnnouncement } from "./player-feedback";
import { SignalPathButton } from "./signal-path";
import { queueStorageKey, saveQueue } from "../engine/persistence";

const fullscreenState = vi.hoisted(() => ({ open: false, listeners: new Set<() => void>() }));

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
let capabilitiesPending = false;
vi.mock("../hooks/use-playback-capabilities", () => ({
  usePlaybackCapabilities: function useTestCapabilities() {
    return useQuery({ queryKey: ["test-capabilities", capabilitiesPending], queryFn: () => (capabilitiesPending ? new Promise<CapabilityTable>(() => undefined) : Promise.resolve(table)), ...(capabilitiesPending ? {} : { initialData: table }) });
  },
}));
let engine: PlayerEngine;
let audio: FakeAudio;
let table: CapabilityTable;
let seq = 0;
function view(children: ReactNode) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>);
}
const ready = (trackId: string, kind: "original" | "derived" = "original"): TrackPlaybackResult => ({ state: "ready", playback: { track_id: trackId, representation: { kind, url: `/api/v1/tracks/${trackId}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: 100 } } });
function createEngine(overrides: Partial<EngineDeps> = {}) {
  return new PlayerEngine({ userId: "test-user", audio, sessionStorage: null, localStorage: null, mediaSession: null, artworkUrl: (id) => `/api/v1/artworks/${id}`, newId: () => `entry-${++seq}`, random: () => 0, now: () => 0, playRecorder: silentPlayRecorder,
    loadPlayback: async (trackId) => ready(trackId), shouldPrefetch: () => false, loadRelease: async () => releaseDetailOf(albumGroup(1).representative), ...overrides,
  });
}
async function settle(): Promise<void> { for (let turn = 0; turn < 8; turn += 1) await Promise.resolve(); }
beforeEach(() => {
  fullscreenState.open = false;
  fullscreenState.listeners.clear();
  capabilitiesPending = false;
  audio = new FakeAudio();
  table = {
    verdictOf: ({ sourceKind, codec }) => sourceKind === "cue_virtual" ? { playable: false, reason: "cue_virtual" } : codec === "alac" ? { playable: false, reason: "browser_cannot_decode" } : { playable: true },
    deliveryOf: ({ sourceKind, codec }) => sourceKind === "cue_virtual" || codec === "alac" ? "derived" : "original",
  };
  vi.stubGlobal("Audio", class { constructor() { return audio; } });
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
  engine = createEngine();
  engine.attach();
});
afterEach(() => { engine.endSession(); vi.useRealTimers(); });

describe("播放栏、全屏、队列与信号路径", () => {
  it("播放栏封面打开全屏；Esc 和收起按钮都关闭全屏", async () => {
    const user = userEvent.setup();
    engine.enqueue([playerTrack("1")]);
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

  it("队列抽屉关闭后焦点返回队列图标", async () => {
    const user = userEvent.setup();
    engine.enqueue([playerTrack("1"), playerTrack("2")]);
    view(<PlayerBar />);
    const opener = screen.getByRole("button", { name: "队列（2）" });
    await user.click(opener);
    const dialog = await screen.findByRole("dialog", { name: "队列（2）" });
    expect(within(dialog).getAllByRole("listitem")).toHaveLength(2);
    await user.click(within(dialog).getByRole("button", { name: "关闭队列" }));
    await waitFor(() => expect(dialog.hasAttribute("open")).toBe(false));
    expect(document.activeElement).toBe(opener);
  });

  it("信号路径弹层只呈现四层取值，不显示解释句", async () => {
    const user = userEvent.setup();
    engine.enqueue([playerTrack("1", { source: { sourceKind: "m4a", codec: "alac", bitDepth: 24, sampleRate: 48000, channels: 2, bitrate: null } })]);
    view(<SignalPathButton />);
    await user.click(screen.getByRole("button", { name: "信号路径：无损" }));
    const dialog = await screen.findByRole("dialog", { name: "信号路径：无损" });
    const rows = within(dialog).getAllByRole("listitem");
    expect(rows.map((row) => row.textContent)).toEqual([
      "数据源ALAC 48kHz 24bit 2ch",
      "服务端ALAC → FLAC",
      "浏览器解码",
      "系统输出系统默认输出",
    ]);
    for (const removedText of [
      "ALAC 转成 FLAC，位深、采样率与声道不变。",
      "原样发送，不转码。",
      "正在取得服务端的交付方式。",
      "由浏览器解码，可能按输出设备重新采样。",
      "网页看不到声卡的实际格式。",
    ]) expect(dialog.textContent).not.toContain(removedText);
  });

  it.each([
    { kind: "network" as const, code: "", inline: "读取失败", announcement: "暂时无法读取音频，已暂停，可以重试。" },
    { kind: "http" as const, code: "derivation_busy", inline: "服务器正忙", announcement: "服务器正忙于转换，已暂停，可以重试。" },
  ])("$inline 故障显示可重试状态", async ({ kind, code, inline, announcement }) => {
    let failed = true;
    engine.endSession();
    engine = createEngine({ loadPlayback: async (trackId) => {
      if (failed) throw kind === "network" ? new ApiError({ kind: "network", requestId: "req-1" }) : new ApiError({ kind: "http", status: 503, code, message: "后端文案", requestId: "req-1" });
      return ready(trackId, "derived");
    } });
    engine.attach();
    engine.enqueue([playerTrack("1")]);
    view(<><PlayerBar /><PlayerAnnouncement /></>);
    await act(async () => { engine.playTracks([playerTrack("1")]); await settle(); });
    expect(screen.getByText(inline, { exact: true }).isConnected).toBe(true);
    expect(screen.getByRole("status").textContent).toBe(announcement);
    failed = false;
    await userEvent.click(screen.getByRole("button", { name: "重试播放" }));
    await waitFor(() => expect(engine.getSnapshot().fault).toBeNull());
    expect(audio.sources).toEqual(["/api/v1/tracks/track-1/audio"]);
    expect(screen.queryByRole("button", { name: "重试播放" })).toBeNull();
  });

  it("自动播放拒绝保持暂停并给出恢复动作", async () => {
    audio.playBehavior = "not-allowed";
    engine.enqueue([playerTrack("1")]);
    view(<><PlayerBar /><PlayerAnnouncement /></>);
    await userEvent.click(screen.getByRole("button", { name: "播放" }));
    expect(await screen.findByText("请按播放")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "暂停" })).toBeNull();
    expect(screen.getByRole("status").textContent).toBe("浏览器阻止了自动播放，请按播放。");
  });

  it("缓冲状态保持暂停动作", async () => {
    engine.enqueue([playerTrack("1")]);
    view(<PlayerBar />);
    await userEvent.click(screen.getByRole("button", { name: "播放" }));
    await waitFor(() => expect(audio.playCalls).toBeGreaterThan(0));
    act(() => { audio.beginPlaying(); audio.emit("waiting"); });
    expect(screen.getByText("正在缓冲")).toBeTruthy();
    expect(screen.getByRole("button", { name: "暂停" })).toBeTruthy();
  });

  it("加载中按钮写“暂停”，按下放弃加载", async () => {
    engine.endSession();
    engine = createEngine({ loadPlayback: () => new Promise(() => undefined) });
    engine.attach();
    view(<PlayerBar />);
    act(() => { engine.playTracks([playerTrack("1")]); });
    expect(engine.getSnapshot().status).toBe("loading");
    await userEvent.click(screen.getByRole("button", { name: "暂停" }));
    expect(engine.getSnapshot().status).toBe("paused");
    expect(screen.getByRole("button", { name: "播放" })).toBeTruthy();
    expect(audio.playCalls).toBe(0);
  });

  it("等待转换时显示“正在准备”，暂停会放弃等待", async () => {
    vi.useFakeTimers();
    engine.endSession();
    const signals: AbortSignal[] = [];
    engine = createEngine({ loadPlayback: (_trackId, signal) => { signals.push(signal); return Promise.resolve<TrackPlaybackResult>({ state: "preparing" }); } });
    engine.attach();
    view(<><PlayerBar /><PlayerAnnouncement /></>);
    await act(async () => { engine.playTracks([playerTrack("1")]); await settle(); });
    expect(screen.getByText("正在准备")).toBeTruthy();
    expect(screen.getByRole("status").textContent).toBe("正在准备：曲目 1，转换完成后开始播放。");
    await act(async () => { vi.advanceTimersByTime(1000); await settle(); });
    expect(signals).toHaveLength(2);
    fireEvent.click(screen.getByRole("button", { name: "暂停" }));
    expect(engine.getSnapshot().status).toBe("paused");
    await act(async () => { vi.advanceTimersByTime(10_000); await settle(); });
    expect(signals).toHaveLength(2);
    expect(audio.playCalls).toBe(0);
  });

  it("无损转换失败的曲目在队列里写明原因", async () => {
    engine.endSession();
    engine = createEngine({ loadPlayback: async () => { throw new ApiError({ kind: "http", status: 409, code: "derivation_failed", message: "后端文案", requestId: "req-1" }); } });
    engine.attach();
    engine.enqueue([playerTrack("1")]);
    view(<PlayerQueue headingId="queue-heading" onClose={() => undefined} />);
    await act(async () => { engine.playEntry(engine.getSnapshot().queue.entries[0]!.entryId); await settle(); });
    expect(screen.getByText("无损转换失败，无法播放这首曲目")).toBeTruthy();
  });

  it("暂时无法读取与自动播放拒绝进入常驻全局公告", async () => {
    engine.endSession();
    let online = false;
    engine = createEngine({ loadPlayback: async (trackId) => { if (!online) throw new ApiError({ kind: "network", requestId: "req-1" }); return ready(trackId); } });
    engine.attach();
    view(<PlayerAnnouncement />);
    const region = screen.getByRole("status");
    await act(async () => { engine.playTracks([playerTrack("1")]); await settle(); });
    expect(region.textContent).toBe("暂时无法读取音频，已暂停，可以重试。");
    online = true;
    audio.playBehavior = "not-allowed";
    await act(async () => { engine.retry(); await settle(); });
    expect(screen.getByRole("status")).toBe(region);
    expect(region.textContent).toBe("浏览器阻止了自动播放，请按播放。");
  });

  it("播放模式循环、静音、音量与下一首可用", async () => {
    engine.enqueue([playerTrack("1"), playerTrack("2")]);
    view(<PlayerBar />);
    for (const [current, next, mode] of [
      ["顺序", "列表循环", "repeat_all"], ["列表循环", "单曲循环", "repeat_one"], ["单曲循环", "随机", "shuffle"], ["随机", "顺序", "sequential"],
    ]) {
      await userEvent.click(screen.getByRole("button", { name: `播放模式：${current}` }));
      expect(engine.getSnapshot().queue.mode).toBe(mode);
      expect(screen.getByRole("button", { name: `播放模式：${next}` })).toBeTruthy();
    }
    await userEvent.click(screen.getByRole("button", { name: "静音" }));
    expect(engine.getSnapshot().muted).toBe(true);
    fireEvent.change(screen.getByRole("slider", { name: "播放器音量" }), { target: { value: "0.4" } });
    expect(engine.getSnapshot().volume).toBe(0.4);
    await userEvent.click(screen.getByRole("button", { name: "下一首" }));
    expect(engine.getSnapshot().current?.track.trackId).toBe("track-2");
  });

  it("刷新恢复临时失败时即使队列为空也能从播放栏重试", async () => {
    engine.endSession();
    const group = albumGroup(1, { medium_count: 1, track_count: 1 });
    const detail = releaseDetailOf(group.representative);
    const track = detail.media[0]?.tracks[0];
    if (track === undefined) throw new Error("缺少恢复用曲目");
    const storage = memoryStorage();
    saveQueue(storage, "test-user", { entries: [{ entryId: "restored-entry", trackId: track.id, releaseId: detail.id, groupId: group.id }], currentEntryId: "restored-entry", mode: "sequential", positionSeconds: 12, history: [] });
    const loadRelease = vi.fn<EngineDeps["loadRelease"]>().mockRejectedValueOnce(new Error("暂时离线")).mockResolvedValue(detail);
    engine = createEngine({ sessionStorage: storage, loadRelease });
    engine.attach();
    view(<PlayerBar />);
    const retry = await screen.findByRole("button", { name: "重试播放" });
    expect(engine.getSnapshot().queue.entries).toHaveLength(0);
    expect(storage.getItem(queueStorageKey("test-user"))).not.toBeNull();
    await userEvent.click(retry);
    await waitFor(() => expect(engine.getSnapshot().queue.entries).toHaveLength(1));
    expect(engine.getSnapshot().status).toBe("paused");
    expect(audio.playCalls).toBe(0);
    expect(screen.queryByRole("button", { name: "重试播放" })).toBeNull();
  });

  it("选曲公告不把加载意图说成已经播放", () => {
    engine.endSession();
    engine = createEngine({ loadPlayback: () => new Promise(() => undefined) });
    engine.attach();
    engine.playTracks([playerTrack("未加载")]);
    view(<PlayerAnnouncement />);
    expect(screen.getByRole("status").textContent).toBe("已选择曲目：曲目 未加载");
    expect(screen.getByRole("status").textContent).not.toContain("正在播放");
    expect(engine.getSnapshot().status).toBe("loading");
    expect(audio.playCalls).toBe(0);
  });

  it("信号路径在取得格式表之前显示读取中", () => {
    engine.enqueue([playerTrack("1", { source: { sourceKind: "m4a", codec: "alac", bitDepth: 16, sampleRate: 44100, channels: 2, bitrate: null } })]);
    capabilitiesPending = true;
    view(<SignalPathButton />);
    const trigger = screen.getByRole("button", { name: "信号路径：无损" });
    fireEvent.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "信号路径：无损" });
    expect(within(dialog).getAllByRole("listitem")[1]?.textContent).toContain("读取中");
    expect(dialog.textContent).not.toContain("原文件直出");
  });

  it("队列重复曲目按 entryId 上移、移除；清空必须确认", async () => {
    const user = userEvent.setup();
    engine.enqueue([playerTrack("1"), playerTrack("1"), playerTrack("2")]);
    const secondId = engine.getSnapshot().queue.entries[1]?.entryId;
    view(<PlayerQueue headingId="queue-heading" onClose={() => undefined} />);
    let rows = screen.getAllByRole("listitem");
    const secondRow = rows[1];
    if (secondRow === undefined) throw new Error("缺少第二项");
    await user.click(within(secondRow).getByRole("button", { name: "上移" }));
    expect(engine.getSnapshot().queue.entries[0]?.entryId).toBe(secondId);
    rows = screen.getAllByRole("listitem");
    const firstRow = rows[0];
    if (firstRow === undefined) throw new Error("缺少第一项");
    await user.click(within(firstRow).getByRole("button", { name: "移除" }));
    expect(engine.getSnapshot().queue.entries).toHaveLength(2);
    expect(engine.getSnapshot().queue.entries.some((entry) => entry.entryId === secondId)).toBe(false);
    await user.click(screen.getByRole("button", { name: "清空队列" }));
    expect(engine.getSnapshot().queue.entries).toHaveLength(2);
    const dialog = screen.getByRole("dialog", { name: "清空播放队列？" });
    await user.click(within(dialog).getByRole("button", { name: "取消" }));
    expect(engine.getSnapshot().queue.entries).toHaveLength(2);
    await user.click(screen.getByRole("button", { name: "清空队列" }));
    await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "清空队列" }));
    expect(engine.getSnapshot().queue.entries).toHaveLength(0);
  });

  it("播放栏长标题保留完整名称；未知时长禁止拖动，媒体事件才能改变播放状态", async () => {
    const longTitle = "长标题".repeat(400);
    engine.enqueue([playerTrack("1", { title: longTitle }), playerTrack("2")]);
    view(<PlayerBar />);
    expect(screen.getByText(longTitle, { exact: true })).toBeTruthy();
    const seek = screen.getByRole("slider", { name: "播放进度" });
    expect((seek as HTMLInputElement).disabled).toBe(true);
    expect(seek.getAttribute("aria-valuetext")).toBe("0:00 / —:—");
    expect(seek.getAttribute("aria-valuetext")).toBe("0:00 / —:—");
    await userEvent.click(screen.getByRole("button", { name: "播放" }));
    await waitFor(() => expect(audio.playCalls).toBeGreaterThan(0));
    act(() => { audio.loadMetadata(120); audio.beginPlaying(); audio.tick(83); });
    expect(screen.getByRole("button", { name: "暂停" })).toBeTruthy();
    expect((seek as HTMLInputElement).disabled).toBe(false);
    expect(seek.getAttribute("aria-valuetext")).toBe("1:23 / 2:00");
    fireEvent.change(seek, { target: { value: "90" } });
    expect(audio.currentTime).toBe(90);
  });
});

describe("专辑播放入口", () => {
  function release() {
    const detail = releaseDetailOf(albumGroup(1, { track_count: 3 }).representative);
    const medium = detail.media[0];
    if (medium === undefined || medium.tracks[0] === undefined) throw new Error("缺少测试曲目");
    const first = medium.tracks[0];
    return { ...detail, media: [{ ...medium, tracks: [{ ...first, id: "track-1", codec: "flac", source_kind: "flac_vorbis_comment" }, { ...first, id: "track-2", codec: "alac", source_kind: "m4a" }, { ...first, id: "track-3", source_kind: "cue_virtual" }] }] };
  }
  it("整张立即播放处理队列容量拒绝，不谎报成功", async () => {
    vi.spyOn(engine, "playTracks").mockReturnValue("queue_full");
    view(<AlbumPlaybackActions release={release()} groupId="group-1" />);
    await userEvent.click(screen.getByRole("button", { name: "播放整张专辑" }));
    expect(screen.getByRole("status").textContent).toContain("队列已满，请先移除一些条目。");
    expect(engine.getSnapshot().queue.entries).toHaveLength(0);
  });
  it("一首都不能播放时禁用整张动作并在头部解释", () => {
    table = { verdictOf: () => ({ playable: false, reason: "format_not_direct" }), deliveryOf: () => null };
    view(<AlbumPlaybackActions release={release()} groupId="group-1" />);
    expect((screen.getByRole("button", { name: "播放整张专辑" }) as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/这个版本没有能播放的曲目/)).toBeTruthy();
  });
  it("整张播放跳过不可播放项，加入队列不打断当前项", async () => {
    view(<AlbumPlaybackActions release={release()} groupId="group-1" />);
    await userEvent.click(screen.getByRole("button", { name: "播放整张专辑" }));
    expect(engine.getSnapshot().queue.entries.map((entry) => entry.track.trackId)).toEqual(["track-1"]);
    expect(screen.getByRole("status").textContent).toContain("跳过 2 首");
    const currentId = engine.getSnapshot().current?.entryId;
    await userEvent.click(screen.getByRole("button", { name: "加入队列" }));
    expect(engine.getSnapshot().queue.entries).toHaveLength(2);
    expect(engine.getSnapshot().current?.entryId).toBe(currentId);
  });
});
