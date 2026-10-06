import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Profiler, useSyncExternalStore, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { albumGroup, releaseDetailOf } from "../../../test/catalog-data";
import { FakeAudio } from "../../../test/fake-audio";
import { playerTrack } from "../../../test/player-data";
import { memoryStorage } from "../../../test/memory-storage";
import { queueStorageKey, saveQueue } from "../engine/persistence";
import { ApiError } from "../../../shared/api";
import { PlayerEngine, type EngineDeps, type PlayerSnapshot } from "../engine/player-engine";
import type { CapabilityTable } from "../engine/capability";
import { AlbumPlaybackActions, TrackPlaybackActions } from "./album-playback-actions";
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
  };
});
vi.mock("../hooks/use-playback-capabilities", () => ({
  usePlaybackCapabilities: function useTestCapabilities() { return useQuery({ queryKey: ["test-capabilities"], queryFn: async () => table, initialData: table }); },
}));
let engine: PlayerEngine;
let audio: FakeAudio;
let table: CapabilityTable;
let seq = 0;
function view(children: ReactNode) {
  return render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>{children}</QueryClientProvider>);
}
function createEngine(overrides: Partial<EngineDeps> = {}) {
  return new PlayerEngine({ userId: "test-user", audio, sessionStorage: null, localStorage: null, mediaSession: null, artworkUrl: (id) => `/api/v1/artworks/${id}`, newId: () => `entry-${++seq}`, random: () => 0, now: () => 0,
    loadPlayback: async (trackId) => ({ track_id: trackId, representation: { kind: "original", url: `/api/v1/tracks/${trackId}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: 100 } }),
    loadRelease: async () => releaseDetailOf(albumGroup(1).representative),
    ...overrides,
  });
}
/** 让已排队的 Promise 回调都执行完。 */
async function settle(): Promise<void> {
  for (let turn = 0; turn < 8; turn += 1) await Promise.resolve();
}
beforeEach(() => {
  audio = new FakeAudio();
  engine = createEngine();
  table = { verdictOf: ({ sourceKind, codec }) => sourceKind === "cue_virtual" ? { playable: false, reason: "cue_virtual" } : codec === "alac" ? { playable: false, reason: "browser_cannot_decode" } : { playable: true } };
  engine.attach();
});
afterEach(() => engine.endSession());

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
  it("暂时无法读取与自动播放被拒都进入全局公告；公告区域常驻，只换里面的文字", async () => {
    engine.endSession();
    let online = false;
    engine = createEngine({ loadPlayback: async (trackId) => {
      if (!online) throw new ApiError({ kind: "network", requestId: "req-1" });
      return { track_id: trackId, representation: { kind: "original", url: `/api/v1/tracks/${trackId}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: 100 } };
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
  it("完整控制支持模式菜单、静音、音量与下一首", async () => {
    const user = userEvent.setup();
    engine.enqueue([playerTrack("1"), playerTrack("2")]);
    view(<PlayerControls />);
    await user.click(screen.getByRole("button", { name: "播放模式：顺序" }));
    await user.click(screen.getByRole("menuitem", { name: "随机" }));
    expect(engine.getSnapshot().queue.mode).toBe("shuffle");
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
  it("正在播放空态不伪造封面、进度或信号路径", () => {
    view(<NowPlayingPage />);
    expect(screen.getByRole("heading", { level: 1, name: "正在播放" })).toBeTruthy();
    expect(screen.getByText(/队列是空的/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "去专辑" })).toBeTruthy();
    expect(screen.queryByRole("slider")).toBeNull();
  });
  it("信号路径分四段，输出未知，不宣称硬件保证", () => {
    engine.enqueue([playerTrack("1", { source: { sourceKind: "m4a", codec: "alac", bitDepth: null, sampleRate: 48000, channels: 2, bitrate: null } })]);
    view(<SignalPath />);
    for (const name of ["源文件", "服务端", "浏览器", "系统输出"]) expect(screen.getByRole("heading", { name })).toBeTruthy();
    expect(screen.getByText("MP4 / M4A")).toBeTruthy();
    expect(screen.getByText("48 kHz")).toBeTruthy();
    expect(screen.getByText(/网页无法得知声卡/)).toBeTruthy();
    expect(document.body.textContent).not.toContain("Bit-perfect");
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
    expect(screen.getByRole("status").textContent).toContain("最多容纳 10,000 条");
    expect(engine.getSnapshot().queue.entries).toHaveLength(0);
  });
  it("一首都不能播放时禁用整张动作并在头部解释", () => {
    table = { verdictOf: () => ({ playable: false, reason: "format_not_direct" }) };
    view(<AlbumPlaybackActions release={release()} groupId="group-1" />);
    expect(screen.getByRole("button", { name: "播放整张专辑" }).getAttribute("disabled")).not.toBeNull();
    expect(screen.getByText(/这个版本没有可以播放的曲目/)).toBeTruthy();
  });
  it("曲目动作只取引擎不订阅快照：调音量、加入队列都不重绘任何一行", () => {
    const detail = release();
    const tracks = detail.media[0]?.tracks ?? [];
    let commits = 0;
    view(<Profiler id="track-actions" onRender={() => { commits += 1; }}>{tracks.map((track) => <TrackPlaybackActions key={track.id} release={detail} track={track} groupId="group-1" />)}</Profiler>);
    commits = 0;
    act(() => { engine.setVolume(0.5); });
    act(() => { engine.enqueue([playerTrack("1")]); });
    expect(engine.getSnapshot().queue.entries).toHaveLength(1);
    expect(commits).toBe(0);
  });
  it("ALAC 和 CUE 的禁用原因可见，不只依赖悬停", () => {
    const detail = release();
    const tracks = detail.media[0]?.tracks ?? [];
    view(<>{tracks.slice(1).map((track) => <TrackPlaybackActions key={track.id} release={detail} track={track} groupId="group-1" />)}</>);
    for (const button of screen.getAllByRole("button", { name: "从这首开始播放" })) expect(button.getAttribute("disabled")).not.toBeNull();
    expect(screen.getByText("当前浏览器不能解码 ALAC")).toBeTruthy();
    expect(screen.getByText("CUE 分轨曲目需要切分，目前不能播放")).toBeTruthy();
  });
  it("单曲更多菜单下一首播放与加入队列，满队列显示原因", async () => {
    const detail = release();
    const track = detail.media[0]?.tracks[0];
    if (track === undefined) throw new Error("缺少测试曲目");
    view(<TrackPlaybackActions release={detail} track={track} groupId="group-1" />);
    await userEvent.click(screen.getByRole("button", { name: "更多" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "下一首播放" }));
    expect(engine.getSnapshot().queue.entries).toHaveLength(1);
    vi.spyOn(engine, "enqueue").mockReturnValue("queue_full");
    await userEvent.click(screen.getByRole("button", { name: "更多" }));
    await userEvent.click(screen.getByRole("menuitem", { name: "加入队列" }));
    expect(screen.getByText("队列已满，请先移除一些条目。")).toBeTruthy();
  });
});
