import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useSyncExternalStore, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { albumGroup, releaseDetailOf } from "../../../test/catalog-data";
import { FakeAudio } from "../../../test/fake-audio";
import { playerTrack } from "../../../test/player-data";
import { silentPlayRecorder } from "../../../test/play-recorder";
import { memoryStorage } from "../../../test/memory-storage";
import { queueStorageKey, saveQueue } from "../engine/persistence";
import { ApiError } from "../../../shared/api";
import { PlayerEngine, type EngineDeps, type PlayerProgress, type PlayerSnapshot } from "../engine/player-engine";
import type { CapabilityTable } from "../engine/capability";
import type { TrackPlaybackResult } from "../api/endpoints";
import { AlbumPlaybackActions } from "./album-playback-actions";
import { PlayerBar } from "./player-bar";
import { PlayerControls } from "./player-controls";
import { PlayerAnnouncement, PlayerFeedback } from "./player-feedback";
import { PlayerQueue } from "./player-queue";
import { SignalPath } from "./signal-path";
import { NowPlayingPage } from "../pages/now-playing-page";

vi.mock("@tanstack/react-router", () => ({ Link: ({ to, children, hash, title }: { to: string; children: ReactNode; hash?: string; title?: string }) => <a href={`${to}${hash === undefined ? "" : `#${hash}`}`} title={title}>{children}</a> }));
vi.mock("../player-provider", () => {
  return {
    usePlayer: function useTestPlayer() { return { engine, snapshot: useSyncExternalStore(engine.subscribe, engine.getSnapshot) }; },
    usePlayerEngine: function useTestPlayerEngine() { return engine; },
    usePlayerSelector: function useTestPlayerSelector<T>(select: (snapshot: PlayerSnapshot) => T) { return useSyncExternalStore(engine.subscribe, () => select(engine.getSnapshot())); },
    usePlayerProgress: function useTestProgress() { return useSyncExternalStore(engine.subscribeProgress, engine.getProgress); },
    usePlayerProgressSelector: function useTestProgressSelector<T>(select: (progress: PlayerProgress) => T) { return useSyncExternalStore(engine.subscribeProgress, () => select(engine.getProgress())); },
  };
});
// 页面级用例不关心歌词内容（面板自己的行为见 lyrics-panel.test.tsx）：歌词查询替换成“没有歌词”，并记录请求了哪些曲目。
const lyricsRequests: string[] = [];
vi.mock("../hooks/use-track-lyrics", () => ({
  useTrackLyrics: function useTestLyrics(trackId: string) {
    lyricsRequests.push(trackId);
    return { status: "success", data: null, error: null, refetch: () => undefined };
  },
}));
// capabilitiesPending 为真时模拟“格式表还没取到”：没有初始数据，请求一直不返回。
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
/** 预检通过：原文件直出或已转换好的派生文件。 */
const ready = (trackId: string, kind: "original" | "derived" = "original"): TrackPlaybackResult => ({ state: "ready", playback: { track_id: trackId, representation: { kind, url: `/api/v1/tracks/${trackId}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: 100 } } });
function createEngine(overrides: Partial<EngineDeps> = {}) {
  return new PlayerEngine({ userId: "test-user", audio, sessionStorage: null, localStorage: null, mediaSession: null, artworkUrl: (id) => `/api/v1/artworks/${id}`, newId: () => `entry-${++seq}`, random: () => 0, now: () => 0, playRecorder: silentPlayRecorder,
    loadPlayback: async (trackId) => ready(trackId),
    shouldPrefetch: () => false,
    loadRelease: async () => releaseDetailOf(albumGroup(1).representative),
    ...overrides,
  });
}
/** 让已排队的 Promise 回调都执行完。 */
async function settle(): Promise<void> {
  for (let turn = 0; turn < 8; turn += 1) await Promise.resolve();
}
beforeEach(() => {
  capabilitiesPending = false;
  audio = new FakeAudio();
  engine = createEngine();
  table = {
    verdictOf: ({ sourceKind, codec }) => sourceKind === "cue_virtual" ? { playable: false, reason: "cue_virtual" } : codec === "alac" ? { playable: false, reason: "browser_cannot_decode" } : { playable: true },
    deliveryOf: ({ sourceKind, codec }) => sourceKind === "cue_virtual" || codec === "alac" ? null : "original",
  };
  engine.attach();
});
afterEach(() => {
  engine.endSession();
  vi.useRealTimers();
});

describe("播放界面", () => {
  it("播放栏长标题保留完整名称；未知时长禁止拖动，媒体事件才能改变播放状态", async () => {
    const long = "长标题".repeat(400);
    engine.enqueue([playerTrack("1", { title: long }), playerTrack("2")]);
    view(<PlayerBar />);
    expect(screen.getByRole("region", { name: "播放器" })).toBeTruthy();
    expect(screen.getByRole("link", { name: long })).toBeTruthy();
    const seek = screen.getByRole("slider", { name: "播放进度" });
    expect(seek.getAttribute("disabled")).not.toBeNull();
    expect(seek.getAttribute("aria-valuetext")).toBe("0:00 / —:—");
    await userEvent.click(screen.getByRole("button", { name: "播放" }));
    await waitFor(() => expect(audio.playCalls).toBeGreaterThan(0));
    act(() => { audio.loadMetadata(120); audio.beginPlaying(); audio.tick(83); });
    expect(screen.getByRole("button", { name: "暂停" })).toBeTruthy();
    expect(seek.getAttribute("disabled")).toBeNull();
    expect(seek.getAttribute("aria-valuetext")).toBe("1:23 / 2:00");
    fireEvent.change(seek, { target: { value: "90" } });
    expect(audio.currentTime).toBe(90);
  });
  it("自动播放拒绝保持暂停并给出恢复动作，底栏不伪装成正在播放", async () => {
    audio.playBehavior = "not-allowed";
    engine.enqueue([playerTrack("1")]);
    view(<><PlayerBar /><PlayerFeedback /></>);
    await userEvent.click(screen.getByRole("button", { name: "播放" }));
    await screen.findByText("浏览器阻止了自动播放，请按播放。");
    expect(screen.queryByRole("button", { name: "暂停" })).toBeNull();
    expect(screen.getByText("请按播放")).toBeTruthy();
  });
  it("缓冲状态保持暂停动作，不把进度当作完成证明", async () => {
    engine.enqueue([playerTrack("1")]);
    view(<PlayerBar />);
    await userEvent.click(screen.getByRole("button", { name: "播放" }));
    await waitFor(() => expect(audio.playCalls).toBeGreaterThan(0));
    act(() => { audio.beginPlaying(); audio.emit("waiting"); });
    expect(screen.getByText("正在缓冲")).toBeTruthy();
    expect(screen.getByRole("button", { name: "暂停" })).toBeTruthy();
  });
  it("加载中按钮写“暂停”，按下就放弃这次加载停在暂停：名称与动作一致", async () => {
    engine.endSession();
    engine = createEngine({ loadPlayback: () => new Promise(() => undefined) });
    engine.attach();
    view(<PlayerControls />);
    act(() => { engine.playTracks([playerTrack("1")]); });
    expect(engine.getSnapshot().status).toBe("loading");
    expect(screen.queryByRole("button", { name: "播放" })).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "暂停" }));
    expect(engine.getSnapshot().status).toBe("paused");
    expect(screen.getByRole("button", { name: "播放" })).toBeTruthy();
    expect(audio.playCalls).toBe(0);
  });
  it("等待转换时显示“正在准备”：按钮写“暂停”，按下就放弃等待并停在暂停；公告说明正在转换", async () => {
    vi.useFakeTimers();
    engine.endSession();
    const signals: AbortSignal[] = [];
    // 立即返回的 202：引擎按最小间隔（1 秒）再次预检，测试用假定时器推进。
    engine = createEngine({ loadPlayback: (_trackId, signal) => { signals.push(signal); return Promise.resolve<TrackPlaybackResult>({ state: "preparing" }); } });
    engine.attach();
    view(<><PlayerBar /><PlayerAnnouncement /></>);
    await act(async () => { engine.playTracks([playerTrack("1")]); await settle(); });
    expect(engine.getSnapshot().status).toBe("preparing");
    expect(screen.getByText("正在准备")).toBeTruthy();
    expect(screen.getByRole("status").textContent).toBe("正在准备：曲目 1，转换完成后开始播放。");
    expect(screen.queryByRole("button", { name: "播放" })).toBeNull();
    await act(async () => { vi.advanceTimersByTime(1000); await settle(); });
    expect(signals).toHaveLength(2);
    fireEvent.click(screen.getByRole("button", { name: "暂停" }));
    expect(engine.getSnapshot().status).toBe("paused");
    expect(screen.getByRole("button", { name: "播放" })).toBeTruthy();
    await act(async () => { vi.advanceTimersByTime(10_000); await settle(); });
    expect(signals).toHaveLength(2);
    expect(audio.playCalls).toBe(0);
  });
  it("服务器正忙于转换：暂停并给出可重试的提示，底栏与公告都说明原因，重试后继续", async () => {
    engine.endSession();
    let busy = true;
    engine = createEngine({ loadPlayback: async (trackId) => {
      if (busy) throw new ApiError({ kind: "http", status: 503, code: "derivation_busy", message: "后端文案", requestId: "req-1" });
      return ready(trackId, "derived");
    } });
    engine.attach();
    view(<><PlayerBar /><PlayerFeedback /><PlayerAnnouncement /></>);
    await act(async () => { engine.playTracks([playerTrack("1")]); await settle(); });
    expect(engine.getSnapshot().fault).toEqual({ kind: "busy" });
    expect(screen.getByText("服务器正忙于转换，请稍后重试。")).toBeTruthy();
    expect(screen.getByText("服务器正忙于转换，进入正在播放页重试")).toBeTruthy();
    // 提示条与全局公告都是 status；公告是隐藏的常驻区域。
    expect(screen.getAllByRole("status").map((region) => region.textContent)).toContain("服务器正忙于转换，请稍后重试。已暂停，可到正在播放页重试。");
    expect(screen.queryByText(/检查网络/)).toBeNull();
    busy = false;
    await act(async () => { await userEvent.click(screen.getByRole("button", { name: "重试播放" })); await settle(); });
    expect(engine.getSnapshot().fault).toBeNull();
    expect(audio.sources).toEqual(["/api/v1/tracks/track-1/audio"]);
    expect(screen.queryByRole("button", { name: "重试播放" })).toBeNull();
  });
  it("无损转换失败的曲目在队列里写明原因", async () => {
    engine.endSession();
    engine = createEngine({ loadPlayback: async () => { throw new ApiError({ kind: "http", status: 409, code: "derivation_failed", message: "后端文案", requestId: "req-1" }); } });
    engine.attach();
    view(<PlayerQueue />);
    await act(async () => { engine.playTracks([playerTrack("1")]); await settle(); });
    expect(screen.getByText("无损转换失败，无法播放这首曲目")).toBeTruthy();
  });
  it("暂时无法读取与自动播放被拒都进入全局公告；公告区域常驻，只换里面的文字", async () => {
    engine.endSession();
    let online = false;
    engine = createEngine({ loadPlayback: async (trackId) => {
      if (!online) throw new ApiError({ kind: "network", requestId: "req-1" });
      return ready(trackId);
    } });
    engine.attach();
    view(<PlayerAnnouncement />);
    const region = screen.getByRole("status");
    await act(async () => { engine.playTracks([playerTrack("1")]); await settle(); });
    expect(region.textContent).toBe("暂时无法读取音频，已暂停。请检查网络后到正在播放页重试。");
    online = true;
    audio.playBehavior = "not-allowed";
    await act(async () => { engine.retry(); await settle(); });
    expect(engine.getSnapshot().fault).toEqual({ kind: "autoplay_blocked" });
    expect(screen.getByRole("status")).toBe(region);
    expect(region.textContent).toBe("浏览器阻止了自动播放，请按播放。");
  });
  it("播放模式按顺序循环四次回到原模式且读屏名同步，静音、音量与下一首仍可用", async () => {
    const user = userEvent.setup();
    engine.enqueue([playerTrack("1"), playerTrack("2")]);
    view(<PlayerControls />);
    for (const [current, next, mode] of [
      ["顺序", "列表循环", "repeat_all"],
      ["列表循环", "单曲循环", "repeat_one"],
      ["单曲循环", "随机", "shuffle"],
      ["随机", "顺序", "sequential"],
    ]) {
      await user.click(screen.getByRole("button", { name: `播放模式：${current}` }));
      expect(engine.getSnapshot().queue.mode).toBe(mode);
      expect(screen.getByRole("button", { name: `播放模式：${next}` })).toBeTruthy();
    }
    await user.click(screen.getByRole("button", { name: "静音" }));
    expect(engine.getSnapshot().muted).toBe(true);
    fireEvent.change(screen.getByRole("slider", { name: "播放器音量" }), { target: { value: "0.4" } });
    expect(engine.getSnapshot().volume).toBe(0.4);
    await user.click(screen.getByRole("button", { name: "下一首" }));
    expect(engine.getSnapshot().current?.track.trackId).toBe("track-2");
  });
  it("刷新恢复临时失败时即使队列为空也能重试，恢复后仍保持暂停", async () => {
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
    view(<NowPlayingPage />);
    const retry = await screen.findByRole("button", { name: "重试播放" });
    expect(engine.getSnapshot().queue.entries).toHaveLength(0);
    expect(storage.getItem(queueStorageKey("test-user"))).not.toBeNull();
    await userEvent.click(retry);
    await screen.findByRole("heading", { name: "队列（1）" });
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
  it("正在播放页截断长标题仍保留完整标题名称与提示", () => {
    const title = "长标题".repeat(400);
    engine.enqueue([playerTrack("1", { title })]);
    view(<NowPlayingPage />);
    const heading = screen.getByRole("heading", { level: 2, name: title });
    expect(heading.textContent).toBe(title);
    expect(heading.getAttribute("title")).toBe(title);
    expect(screen.getByRole("button", { name: "播放" })).toBeTruthy();
  });
  it("正在播放的长艺人与专辑分开截断，完整链接名称与队列操作名称保留", () => {
    const title = "长曲目".repeat(400);
    const artist = "长艺人".repeat(400);
    const albumTitle = "长专辑".repeat(400);
    engine.enqueue([playerTrack("1", { title, artist, albumTitle })]);
    view(<NowPlayingPage />);
    const album = screen.getByRole("link", { name: albumTitle });
    expect(album.textContent).toBe(albumTitle);
    expect(album.getAttribute("title")).toBe(albumTitle);
    expect(album.parentElement?.tagName).toBe("P");
    const artistText = screen.getByText(artist, { exact: true });
    expect(artistText.getAttribute("title")).toBe(artist);
    expect(artistText).not.toBe(album.parentElement);
    const row = within(screen.getByRole("region", { name: "队列（1）" })).getByRole("listitem");
    expect(within(row).getByRole("group", { name: `第 1 项操作：${title}` })).toBeTruthy();
    expect(within(row).getByTitle(title).textContent).toContain(title);
    expect(within(row).getByTitle(`${artist} — ${albumTitle}`).textContent).toBe(`${artist} — ${albumTitle}`);
    expect(screen.getByRole("button", { name: "播放" })).toBeTruthy();
  });
  it("正在播放页为当前曲目放歌词面板：封面、歌词、标题与控制依次出现；没有歌词时如实说明", () => {
    lyricsRequests.length = 0;
    engine.enqueue([playerTrack("1", { title: "第一首" }), playerTrack("2")]);
    view(<NowPlayingPage />);
    const lyrics = screen.getByRole("region", { name: "歌词" });
    expect(within(lyrics).getByText("这首曲目没有内嵌歌词")).toBeTruthy();
    expect(new Set(lyricsRequests)).toEqual(new Set(["track-1"]));
    // DOM 顺序即阅读与焦点顺序：封面 → 歌词 → 标题与控制。
    const cover = screen.getByLabelText("没有可用封面");
    const title = screen.getByRole("heading", { level: 2, name: "第一首" });
    expect(cover.compareDocumentPosition(lyrics) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(lyrics.compareDocumentPosition(title) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    // 换到下一首后歌词面板跟着换曲目。
    act(() => engine.next());
    expect(lyricsRequests.at(-1)).toBe("track-2");
  });
  it("正在播放空态不伪造封面、进度或信号路径", () => {
    view(<NowPlayingPage />);
    expect(screen.getByRole("heading", { level: 1, name: "正在播放" })).toBeTruthy();
    expect(screen.getByText(/队列是空的/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "去专辑" })).toBeTruthy();
    expect(screen.queryByRole("slider")).toBeNull();
    // 队列为空时没有当前曲目，也就没有歌词面板。
    expect(screen.queryByRole("region", { name: "歌词" })).toBeNull();
  });
  it("信号路径分四段，输出未知，不宣称硬件保证", () => {
    engine.enqueue([playerTrack("1", { source: { sourceKind: "m4a", codec: "alac", bitDepth: null, sampleRate: 48000, channels: 2, bitrate: null } })]);
    view(<SignalPath />);
    for (const name of ["源文件", "服务端", "浏览器", "系统输出"]) expect(screen.getByRole("heading", { name })).toBeTruthy();
    expect(screen.getByText("MP4 / M4A")).toBeTruthy();
    expect(screen.getByText("48 kHz")).toBeTruthy();
    expect(screen.getByText(/网页看不到声卡/)).toBeTruthy();
    expect(document.body.textContent).not.toContain("Bit-perfect");
  });
  it("信号路径按交付方式区分直出与无损转换，不展示 PCM 校验细节", () => {
    engine.enqueue([playerTrack("1", { source: { sourceKind: "m4a", codec: "alac", bitDepth: 24, sampleRate: 96000, channels: 2, bitrate: null } })]);
    table = { ...table, deliveryOf: () => "derived" };
    const derived = view(<SignalPath />);
    const stage = screen.getByRole("heading", { name: "服务端" }).closest("li");
    expect(stage?.textContent).toContain("无损转换");
    expect(stage?.textContent).toContain("ALAC 转成 FLAC，位深、采样率与声道不变。");
    expect(stage?.textContent).not.toContain("PCM");
    expect(stage?.textContent).not.toContain("原文件直出");
    derived.unmount();

    engine.clearQueue();
    engine.enqueue([playerTrack("2")]);
    table = { ...table, deliveryOf: () => "original" };
    view(<SignalPath />);
    const original = screen.getByRole("heading", { name: "服务端" }).closest("li");
    expect(original?.textContent).toContain("原文件直出");
    expect(original?.textContent).not.toContain("无损转换");
  });
  it("信号路径在取得格式表之前不替曲目说“原文件直出”", () => {
    engine.enqueue([playerTrack("1", { source: { sourceKind: "m4a", codec: "alac", bitDepth: 16, sampleRate: 44100, channels: 2, bitrate: null } })]);
    capabilitiesPending = true;
    view(<SignalPath />);
    const stage = screen.getByRole("heading", { name: "服务端" }).closest("li");
    expect(stage?.textContent).toContain("读取中");
    expect(stage?.textContent).not.toContain("原文件直出");
  });
  it("队列重复曲目按 entryId 上移、移除；清空必须确认", async () => {
    const user = userEvent.setup();
    engine.enqueue([playerTrack("1"), playerTrack("1"), playerTrack("2")]);
    const secondId = engine.getSnapshot().queue.entries[1]?.entryId;
    view(<PlayerQueue />);
    let rows = screen.getAllByRole("listitem");
    const secondRow = rows[1];
    if (secondRow === undefined) throw new Error("缺少第二项");
    const move = within(secondRow).getByRole("button", { name: "上移" });
    move.focus();
    await user.keyboard("{Enter}");
    expect(engine.getSnapshot().queue.entries[0]?.entryId).toBe(secondId);
    rows = screen.getAllByRole("listitem");
    const firstRow = rows[0];
    if (firstRow === undefined) throw new Error("缺少第一项");
    within(firstRow).getByRole("button", { name: "移除" }).focus();
    await user.keyboard("{Enter}");
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
});

describe("专辑播放入口", () => {
  function release() {
    const detail = releaseDetailOf(albumGroup(1, { track_count: 3 }).representative);
    const medium = detail.media[0];
    if (medium === undefined || medium.tracks[0] === undefined) throw new Error("缺少测试曲目");
    const first = medium.tracks[0];
    return { ...detail, media: [{ ...medium, tracks: [{ ...first, id: "track-1", codec: "flac", source_kind: "flac_vorbis_comment" }, { ...first, id: "track-2", codec: "alac", source_kind: "m4a" }, { ...first, id: "track-3", source_kind: "cue_virtual" }] }] };
  }
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
  it("整张立即播放也处理队列容量拒绝，不谎报成功", async () => {
    vi.spyOn(engine, "playTracks").mockReturnValue("queue_full");
    view(<AlbumPlaybackActions release={release()} groupId="group-1" />);
    await userEvent.click(screen.getByRole("button", { name: "播放整张专辑" }));
    expect(screen.getByRole("status").textContent).toContain("队列已满，请先移除一些条目。");
    expect(engine.getSnapshot().queue.entries).toHaveLength(0);
  });
  it("一首都不能播放时禁用整张动作并在头部解释", () => {
    table = { verdictOf: () => ({ playable: false, reason: "format_not_direct" }), deliveryOf: () => null };
    view(<AlbumPlaybackActions release={release()} groupId="group-1" />);
    expect(screen.getByRole("button", { name: "播放整张专辑" }).getAttribute("disabled")).not.toBeNull();
    expect(screen.getByText(/这个版本没有能播放的曲目/)).toBeTruthy();
  });
});
