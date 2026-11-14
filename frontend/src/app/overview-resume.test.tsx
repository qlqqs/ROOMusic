import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RecentReleaseDTO } from "../features/listening";
import { playerTracksOf } from "../features/player";
import { albumGroup, catalogDataOf, mediaOf, releaseDetailOf } from "../test/catalog-data";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { FakeAudio } from "../test/fake-audio";
import { installCatalogApi } from "../test/fake-catalog";
import { fakeZoneServer, type FakeZoneServer } from "../test/fake-zones";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

// 概览的“继续播放”与最近播放卡片上的播放键（第 29、15 条）：完整应用，播放区服务端与音频元素是替身。

const zones = vi.hoisted(() => ({ server: undefined as FakeZoneServer | undefined }));
vi.mock("../features/player/zones/api", async (original) => {
  const { delegatingZoneApi } = await import("../test/fake-zones");
  return {
    ...(await original<typeof import("../features/player/zones/api")>()),
    browserZoneApi: delegatingZoneApi(() => {
      if (zones.server === undefined) throw new Error("缺少播放区替身");
      return zones.server;
    }),
  };
});

beforeEach(() => {
  // 服务端只凭 ID 记队列：把 ID 还原成这个版本的曲目，播放区里的当前曲目才有真实的专辑与曲名。
  const known = new Map(playerTracksOf(release, group.id).map((entry) => [entry.trackId, entry]));
  zones.server = fakeZoneServer({
    trackOf: (trackId) => {
      const found = known.get(trackId);
      if (found === undefined) throw new Error(`替身里没有曲目 ${trackId}`);
      return found;
    },
  });
  vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } });
  sessionStorage.clear();
  localStorage.clear();
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const recentPath = "/api/v1/me/plays/recent-releases";
const group = albumGroup(7, { title: "Goldberg Variations", artist: "Glenn Gould", track_count: 4 });
const release = releaseDetailOf(group.representative);
const tracks = release.media.flatMap((medium) => medium.tracks);

function track(index: number) {
  const found = tracks[index];
  if (found === undefined) throw new Error(`没有第 ${index + 1} 首`);
  return found;
}

function recentItem(lastTrack: { id: string; title: string } = { id: track(2).id, title: track(2).title }): RecentReleaseDTO {
  return { group_id: group.id, release: group.representative, last_played_at: new Date(Date.now() - 3 * 60_000).toISOString(), last_track: lastTrack };
}

function setup(items: readonly RecentReleaseDTO[]) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  installOverviewApi(api);
  const catalog = catalogDataOf([group]);
  catalog.releases.set(release.id, release);
  installCatalogApi(api, catalog);
  api.on("GET", recentPath, () => jsonResponse(200, { items, has_history: items.length > 0 }));
  return api;
}

const server = () => {
  if (zones.server === undefined) throw new Error("缺少播放区替身");
  return zones.server;
};

describe("概览的“继续播放”", () => {
  it("没有当前曲目也没有播放记录时不出现", async () => {
    const api = setup([]);
    renderApp();
    await screen.findByText("还没有播放记录。曲目听过一半就会记在这里。");
    await waitFor(() => expect(api.requestsTo("GET", recentPath)).toHaveLength(1));
    expect(screen.queryByRole("region", { name: "继续播放" })).toBeNull();
    expect(screen.queryByRole("region", { name: "正在播放" })).toBeNull();
  });

  it("没有当前曲目时取最近播放的第一张：写专辑、艺人与上次那首和相对时间，按下从那一首开始放整张", async () => {
    setup([recentItem()]);
    renderApp();
    const card = await screen.findByRole("region", { name: "继续播放" });
    expect(within(card).getByRole("link", { name: "Goldberg Variations" }).getAttribute("href")).toBe(`/albums/${group.id}?release=${release.id}`);
    expect(within(card).getByText(`Glenn Gould · ${track(2).title}`)).toBeTruthy();
    expect(within(card).getByText("3 分钟前")).toBeTruthy();
    const play = within(card).getByRole("button", { name: `从《${track(2).title}》继续播放` });
    await waitFor(() => expect(play.hasAttribute("disabled")).toBe(false));

    await userEvent.click(play);
    await waitFor(() => expect(server().queueIds()).toEqual(tracks.map((entry) => entry.id)));
    expect(server().zone().current?.track.trackId).toBe(track(2).id);

    // 开始播放后这一块换成当前曲目：标题“正在播放”，按钮暂停的是播放器，不是重新开始。
    const playing = await screen.findByRole("region", { name: "正在播放" });
    expect(within(playing).getByText(`Glenn Gould · ${track(2).title}`)).toBeTruthy();
    const commands = server().commands.length;
    await userEvent.click(within(playing).getByRole("button", { name: "暂停《Goldberg Variations》" }));
    await waitFor(() => expect(server().zone().intent).toBe("pause"));
    expect(server().commands.length).toBe(commands + 1);
    expect(server().queueIds()).toHaveLength(tracks.length);
    const paused = await screen.findByRole("region", { name: "继续播放" });
    await userEvent.click(within(paused).getByRole("button", { name: "继续播放《Goldberg Variations》" }));
    await waitFor(() => expect(server().zone().intent).toBe("play"));
    expect(server().zone().current?.track.trackId).toBe(track(2).id);
  });

  it("上次那首现在不在这个版本里时从第一首开始", async () => {
    setup([recentItem({ id: "00000000-0000-4000-8000-0000000000ff", title: "已不在的曲目" })]);
    renderApp();
    const card = await screen.findByRole("region", { name: "继续播放" });
    const play = within(card).getByRole("button", { name: "从《已不在的曲目》继续播放" });
    await waitFor(() => expect(play.hasAttribute("disabled")).toBe(false));
    await userEvent.click(play);
    await waitFor(() => expect(server().zone().current?.track.trackId).toBe(track(0).id));
  });

  it("版本里没有能播放的曲目时就地说明，不动队列", async () => {
    const api = setup([recentItem()]);
    const unplayable = { ...release, media: mediaOf(group.representative, () => ({ codec: "unknown" })) };
    api.on("GET", `/api/v1/releases/${release.id}`, () => jsonResponse(200, unplayable));
    renderApp();
    const card = await screen.findByRole("region", { name: "继续播放" });
    const play = within(card).getByRole("button", { name: `从《${track(2).title}》继续播放` });
    await waitFor(() => expect(play.hasAttribute("disabled")).toBe(false));
    await userEvent.click(play);
    expect((await within(card).findByRole("status")).textContent).toBe("《Goldberg Variations》没有能播放的曲目。");
    expect(server().queueIds()).toEqual([]);
  });
});

describe("最近播放卡片上的播放键", () => {
  it("从上次播放的那一首开始放这张专辑", async () => {
    setup([recentItem()]);
    renderApp();
    const panel = await screen.findByRole("region", { name: "最近播放" });
    const play = await within(panel).findByRole("button", { name: "播放《Goldberg Variations》" });
    await waitFor(() => expect(play.hasAttribute("disabled")).toBe(false));
    await userEvent.click(play);
    await waitFor(() => expect(server().queueIds()).toEqual(tracks.map((entry) => entry.id)));
    expect(server().zone().current?.track.trackId).toBe(track(2).id);
  });

  it("读取版本失败时在横排下方说明并可重试", async () => {
    const api = setup([recentItem()]);
    api.on("GET", `/api/v1/releases/${release.id}`, () => jsonResponse(500, { error: { code: "internal_error", message: "服务器内部错误" }, request_id: "req-1" }));
    renderApp();
    const panel = await screen.findByRole("region", { name: "最近播放" });
    const play = await within(panel).findByRole("button", { name: "播放《Goldberg Variations》" });
    await waitFor(() => expect(play.hasAttribute("disabled")).toBe(false));
    await userEvent.click(play);
    const alert = await within(panel).findByRole("alert");
    expect(within(alert).getByText("无法开始播放")).toBeTruthy();
    api.on("GET", `/api/v1/releases/${release.id}`, () => jsonResponse(200, release));
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    await waitFor(() => expect(server().zone().current?.track.trackId).toBe(track(2).id));
    expect(within(panel).queryByRole("alert")).toBeNull();
  });
});
