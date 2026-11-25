import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../../features/auth";
import type { TrackSummaryDTO } from "../../features/catalog";
import type { PersonalQueueSource } from "../../features/personal";
import { decodeFavoriteQueue, decodePlaylistQueue, type FavoriteQueueDTO, type PlaylistQueueDTO } from "../../features/personal/api/decoders";
import { PlayerProvider } from "../../features/player";
import { sampleBody } from "../../test/api-samples";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { playerHarness } from "../../test/fake-zones";
import { jsonResponse } from "../../test/http";
import { playerTrack } from "../../test/player-data";
import { PersonalPlayAll } from "../routes/personal-playback";
import { PlaybackOriginProvider, useNowPlayingOrigin } from "./playback-origin";

const dispose: (() => void)[] = [];
afterEach(() => {
  for (const cleanup of dispose.splice(0)) cleanup();
  vi.restoreAllMocks();
});

const favoriteSample = decodeFavoriteQueue(sampleBody("favorites/queue"));
const playlistSample = decodePlaylistQueue(sampleBody("playlists/queue"));
const playlistId = playlistSample.playlist.id;
const favorites: PersonalQueueSource = { kind: "favorites" };
const playlist: PersonalQueueSource = { kind: "playlist", playlistId };
const pathOf = (source: Exclude<PersonalQueueSource, { kind: "ratings" }>) =>
  source.kind === "favorites" ? "/api/v1/me/favorites/tracks/queue" : `/api/v1/me/playlists/${source.playlistId}/queue`;

function track(index: number): TrackSummaryDTO {
  const base = favoriteSample.items[0];
  if (base === undefined) throw new Error("收藏队列样本缺少曲目");
  return { ...base, track_id: `00000000-0000-4000-8000-${String(index).padStart(12, "0")}`, title: `合成曲目 ${index}`, source: { ...base.source } };
}

function queueResponse(source: Exclude<PersonalQueueSource, { kind: "ratings" }>, items: TrackSummaryDTO[]): FavoriteQueueDTO | PlaylistQueueDTO {
  const counts = {
    items, total: items.length, missing_count: 0, eligible_count: items.length,
    returned_count: items.length, omitted_count: 0, limit: 10_000 as const, truncated: false,
  };
  return source.kind === "favorites"
    ? { ...counts, revision: favoriteSample.revision }
    : { ...counts, playlist: { ...playlistSample.playlist, id: source.playlistId, track_count: items.length, over_limit_count: 0 } };
}

/** 显示外壳侧栏读到的“正在播放的来源”。 */
function Probe() {
  const { key, playing } = useNowPlayingOrigin();
  return <output aria-label="来源">{key === null ? "无" : `${key}${playing ? " 播放中" : " 未在播放"}`}</output>;
}

async function setup() {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  const controller = createSessionController({ channel: null });
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("会话未就绪");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const player = playerHarness();
  dispose.push(() => { player.engine.endSession(); controller.dispose(); client.clear(); });
  const view = (children: ReactNode) => render(
    <QueryClientProvider client={client}><SessionProvider controller={controller}>
      <ViewerScopeProvider viewer={state.viewer} generation={state.generation}>
        <PlayerProvider userId={state.viewer.id} controller={player.engine}>
          <PlaybackOriginProvider>{children}<Probe /></PlaybackOriginProvider>
        </PlayerProvider>
      </ViewerScopeProvider>
    </SessionProvider></QueryClientProvider>,
  );
  return { api, player, view };
}

const origin = () => screen.getByRole("status", { name: "来源" }).textContent;

async function playAll(name = "播放全部") {
  const button = screen.getByRole("button", { name });
  await waitFor(() => expect(button.hasAttribute("disabled")).toBe(false));
  await userEvent.click(button);
}

describe("侧栏“正在播放”标记的来源", () => {
  it("歌单的“播放全部”之后标出这个歌单，元素出声才算播放中；暂停时仍标出、不再跳动", async () => {
    const { api, player, view } = await setup();
    const items = [track(1), track(2), track(3)];
    api.on("GET", pathOf(playlist), () => jsonResponse(200, queueResponse(playlist, items)));
    view(<PersonalPlayAll source={playlist} />);
    expect(origin()).toBe("无");
    await playAll();
    await player.queued(3);
    await waitFor(() => expect(origin()).toBe(`playlist:${playlistId} 未在播放`));
    await player.sounding();
    await waitFor(() => expect(origin()).toBe(`playlist:${playlistId} 播放中`));
    act(() => player.engine.pause());
    await waitFor(() => expect(origin()).toBe(`playlist:${playlistId} 未在播放`));
  });

  it("“我喜欢的音乐”的“播放全部”记为 favorites", async () => {
    const { api, player, view } = await setup();
    api.on("GET", pathOf(favorites), () => jsonResponse(200, queueResponse(favorites, [track(4), track(5)])));
    view(<PersonalPlayAll source={favorites} />);
    await playAll();
    await player.queued(2);
    await waitFor(() => expect(origin()).toBe("favorites 未在播放"));
  });

  it("在这一段之后追加曲目仍标出；放到追加的曲目时不再标出", async () => {
    const { api, player, view } = await setup();
    const items = [track(1), track(2)];
    api.on("GET", pathOf(playlist), () => jsonResponse(200, queueResponse(playlist, items)));
    view(<PersonalPlayAll source={playlist} />);
    await playAll();
    await player.queued(2);
    act(() => { player.engine.enqueue([playerTrack("追加")]); });
    await player.queued(3);
    await waitFor(() => expect(origin()).toBe(`playlist:${playlistId} 未在播放`));
    const appended = player.engine.getSnapshot().queue.entries[2];
    if (appended === undefined) throw new Error("没有追加的条目");
    act(() => player.engine.playEntry(appended.entryId));
    await waitFor(() => expect(player.engine.getSnapshot().queue.currentEntryId).toBe(appended.entryId));
    await waitFor(() => expect(origin()).toBe("无"));
  });

  it("在这一段里插入曲目（下一首播放）后不再标出", async () => {
    const { api, player, view } = await setup();
    api.on("GET", pathOf(playlist), () => jsonResponse(200, queueResponse(playlist, [track(1), track(2), track(3)])));
    view(<PersonalPlayAll source={playlist} />);
    await playAll();
    await player.queued(3);
    await waitFor(() => expect(origin()).not.toBe("无"));
    act(() => { player.engine.playNext([playerTrack("插入")]); });
    await player.queued(4);
    await waitFor(() => expect(origin()).toBe("无"));
  });

  it("之后换成别的队列（播放专辑）后不再标出", async () => {
    const { api, player, view } = await setup();
    api.on("GET", pathOf(playlist), () => jsonResponse(200, queueResponse(playlist, [track(1), track(2)])));
    view(<PersonalPlayAll source={playlist} />);
    await playAll();
    await player.queued(2);
    await waitFor(() => expect(origin()).not.toBe("无"));
    act(() => { player.engine.playTracks([playerTrack("专辑甲"), playerTrack("专辑乙"), playerTrack("专辑丙")]); });
    await player.queued(3);
    await waitFor(() => expect(origin()).toBe("无"));
  });
});
