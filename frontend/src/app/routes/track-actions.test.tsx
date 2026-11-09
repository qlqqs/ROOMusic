import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Profiler, type ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../../features/auth";
import { FavoriteStatusScope } from "../../features/personal";
import { MAX_QUEUE_ENTRIES, PlayerProvider, type PlayerTrack } from "../../features/player";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { playerHarness } from "../../test/fake-zones";
import { historyItem } from "../../test/history-data";
import { TrackActions } from "./track-actions";
import { playerTrackOfSummary } from "./track-playback";

const dispose: (() => void)[] = [];
afterEach(() => { for (const cleanup of dispose.splice(0)) cleanup(); });

const tracks = [
  playerTrackOfSummary(historyItem(1)),
  playerTrackOfSummary(historyItem(2, { source: { ...historyItem(2).source, source_kind: "m4a", codec: "alac" } })),
  playerTrackOfSummary(historyItem(3, { source: { ...historyItem(3).source, source_kind: "cue_virtual" } })),
];

/** 保留原播放器测试的能力场景（服务端格式表里没有 ALAC）；真实队列与心形组件不替换成桩。 */
const formats = { items: [{ source_kind: "flac_vorbis_comment", codec: "flac" }] };

async function setup(items: readonly PlayerTrack[]) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  api.on("GET", "/api/v1/playback/formats", () => new Response(JSON.stringify(formats), { status: 200, headers: { "Content-Type": "application/json" } }));
  const controller = createSessionController({ channel: null });
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("会话未就绪");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  const ids = items.map((track) => track.trackId);
  const player = playerHarness();
  const { engine } = player;
  dispose.push(() => { engine.endSession(); controller.dispose(); client.clear(); });
  const view = (children: ReactNode) => render(
    <QueryClientProvider client={client}>
      <SessionProvider controller={controller}>
        <ViewerScopeProvider viewer={state.viewer} generation={state.generation}>
          <PlayerProvider userId={state.viewer.id} controller={engine}>
            <FavoriteStatusScope trackIds={ids}>{children}</FavoriteStatusScope>
          </PlayerProvider>
        </ViewerScopeProvider>
      </SessionProvider>
    </QueryClientProvider>,
  );
  return { engine, player, view };
}

const actions = (track: PlayerTrack) => <TrackActions key={track.trackId} track={track} present playLabel="从这首开始播放" retryHint="请在专辑头部重试。" />;

async function ready() {
  await waitFor(() => {
    for (const heart of screen.getAllByRole("button", { name: /^收藏《/ })) expect(heart.hasAttribute("disabled")).toBe(false);
  });
}

async function selectAction(name: string) {
  await userEvent.click(screen.getByRole("button", { name: "更多操作" }));
  await userEvent.click(screen.getByRole("menuitem", { name }));
}

// 从 player-ui.test.tsx 迁到 app：跨功能的曲目动作现在由 TrackActions 拥有，player 不反向依赖 app 或 personal。
describe("统一曲目行动作", () => {
  it("只取引擎不订阅快照：调音量、加入队列都不重绘任何一行", async () => {
    const { engine, player, view } = await setup(tracks);
    let commits = 0;
    view(<Profiler id="track-actions" onRender={() => { commits += 1; }}>{tracks.map(actions)}</Profiler>);
    await ready();
    commits = 0;
    act(() => { engine.setVolume(0.5); });
    act(() => { engine.enqueue(tracks.slice(0, 1)); });
    await player.queued(1);
    expect(commits).toBe(0);
  });

  it.each([
    { index: 1, reason: "这种格式目前不能播放" },
    { index: 2, reason: "CUE 分轨曲目需要切分，目前不能播放" },
  ])("$reason：原因可见且与播放按钮关联，更多和收藏可用，仅播放项禁用", async ({ index, reason }) => {
    const track = tracks[index];
    if (track === undefined) throw new Error("缺少测试曲目");
    const { engine, view } = await setup([track]);
    view(actions(track));
    await ready();
    const play = screen.getByRole("button", { name: "从这首开始播放" });
    expect(play.hasAttribute("disabled")).toBe(true);
    const description = screen.getByText(reason);
    expect(play.getAttribute("aria-describedby")).toBe(description.id);
    expect(description.hidden).toBe(false);
    const more = screen.getByRole("button", { name: "更多操作" });
    expect(more.hasAttribute("disabled")).toBe(false);
    await userEvent.click(more);
    for (const name of ["下一首播放", "加入队列"]) expect(screen.getByRole("menuitem", { name }).hasAttribute("disabled")).toBe(true);
    expect(screen.getByRole("menuitem", { name: "添加到歌单…" }).hasAttribute("disabled")).toBe(false);
    expect(engine.getSnapshot().queue.entries).toHaveLength(0);
  });

  it("更多菜单下一首播放与加入队列可用，满队列分别提示原因且不截断", async () => {
    const track = tracks[0];
    if (track === undefined) throw new Error("缺少测试曲目");
    const { engine, player, view } = await setup([track]);
    view(actions(track));
    await ready();
    await selectAction("下一首播放");
    await waitFor(() => expect(engine.getSnapshot().queue.entries.map((entry) => entry.track.trackId)).toEqual([track.trackId]));
    expect(screen.getByRole("status").textContent).toBe("已设为下一首播放。");
    await selectAction("加入队列");
    await waitFor(() => expect(engine.getSnapshot().queue.entries.map((entry) => entry.track.trackId)).toEqual([track.trackId, track.trackId]));
    expect(screen.getByRole("status").textContent).toBe("已加入队列。");
    act(() => { engine.enqueue(Array.from({ length: MAX_QUEUE_ENTRIES - 2 }, () => track)); });
    await player.queued(MAX_QUEUE_ENTRIES);
    const before = engine.getSnapshot().queue;
    const sent = player.server.commands.length;
    for (const name of ["下一首播放", "加入队列"]) {
      await selectAction(name);
      expect(screen.getByRole("status").textContent).toBe("队列已满，请先移除一些条目。");
      expect(player.server.commands).toHaveLength(sent);
      expect(engine.getSnapshot().queue).toBe(before);
      expect(engine.getSnapshot().queue.entries).toHaveLength(MAX_QUEUE_ENTRIES);
    }
  });
});
