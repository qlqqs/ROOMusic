import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { artistCatalogState, artistDataOf, artistTestId, primaryArtistId, respondToArtists, seedArtist } from "../test/artist-data";
import { installArtistsApi, installFakeApi, installFakeSession } from "../test/fake-api";
import { FakeAudio } from "../test/fake-audio";
import { fakeZoneServer, type FakeZoneServer } from "../test/fake-zones";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";
import { playerTrackOfSummary } from "../features/player";

const zones = vi.hoisted(() => ({ server: undefined as FakeZoneServer | undefined }));
vi.mock("../features/player/zones/api", async (original) => {
  const { delegatingZoneApi } = await import("../test/fake-zones");
  return { ...await original<typeof import("../features/player/zones/api")>(), browserZoneApi: delegatingZoneApi(() => { if (zones.server === undefined) throw new Error("缺少播放区替身"); return zones.server; }) };
});

let audios: FakeAudio[];
beforeEach(() => {
  audios = [];
  vi.stubGlobal("Audio", class { constructor() { const audio = new FakeAudio(); audios.push(audio); return audio; } });
  sessionStorage.clear();
  localStorage.clear();
});

it("I4 合并成功经真实失效链刷新播放器与全屏里的艺人链接，不重新加载音频", async () => {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: "curator" });
  const data = installArtistsApi(api, artistDataOf(), session);
  const primary = primaryArtistId();
  const detail = data.details.get(primary);
  const track = data.tracks.get(`${primary}:track_artist`)?.find((item) => item.present);
  if (!detail || !track) throw new Error("真实艺人夹具需要详情与可播放曲目");
  // 默认装配的命令只带曲目 ID：替身按 ID 还原成这首曲目（与页面放进队列的是同一首）。
  const server = fakeZoneServer({ trackOf: () => playerTrackOfSummary(track) });
  zones.server = server;
  const source = seedArtist(data, { ...detail.artist, id: artistTestId(9900), name: "合并前的演唱者" });
  track.artist = source.artist.name;
  track.artist_refs = [{ artist_id: source.artist.id, name: source.artist.name, display_name: source.artist.name }];
  // 固定 epoch：只由治理成功后的 useArtistDataRefresh 触发，不能借状态轮询碰巧刷新的结果证明链路。
  const state = artistCatalogState(data);
  api.on("GET", "/api/v1/catalog/status", () => jsonResponse(200, state));
  api.on("POST", `/api/v1/artists/${primary}/merge`, (request) => {
    const result = respondToArtists(data, request.method, new URL(request.path, "http://localhost"), request.body, request.headers.get("Idempotency-Key"));
    if (result.status === 200) track.artist_refs = [{ artist_id: primary, name: source.artist.name, display_name: detail.artist.name }];
    return jsonResponse(result.status, result.body);
  });
  // I4 暂无真实外层样本，按主会话裁决返回现有安全 TrackSummary，保留原署名而更新身份链接。
  api.on("POST", "/api/v1/tracks/summaries", () => jsonResponse(200, { items: [track] }));
  const user = userEvent.setup();
  renderApp({ path: `/artists/${primary}` });
  const play = await screen.findByRole<HTMLButtonElement>("button", { name: `播放 ${track.title}` });
  await waitFor(() => expect(play.disabled).toBe(false));
  await user.click(play);
  await waitFor(() => expect(audios.find((element) => element.src !== "" && element.src === server.zone().stream?.url)).toBeDefined());
  const audio = audios.find((element) => element.src === server.zone().stream?.url);
  if (audio === undefined) throw new Error("没有在放的元素");
  act(() => { audio.beginPlaying(); audio.tick(37); });
  const bar = within(screen.getByRole("region", { name: "播放器" }));
  expect(bar.getByRole("link", { name: source.artist.name }).getAttribute("href")).toBe(`/artists/${source.artist.id}`);
  expect(api.requestsTo("POST", "/api/v1/tracks/summaries")).toHaveLength(0);
  const before = { sources: [...audio.sources], load: audio.loadCalls, play: audio.playCalls, pause: audio.pauseCalls };
  const commands = server.commands.length;
  await user.click(screen.getByRole("button", { name: "合并艺人…" }));
  const dialog = within(await screen.findByRole("dialog", { name: "合并艺人" }));
  await user.type(dialog.getByRole("textbox", { name: "查找艺人" }), `${source.artist.name}{Enter}`);
  await user.click(await dialog.findByRole("radio", { name: new RegExp(source.artist.name) }));
  await waitFor(() => expect(dialog.getByRole<HTMLButtonElement>("button", { name: "合并" }).disabled).toBe(false));
  await user.click(dialog.getByRole("button", { name: "合并" }));
  await waitFor(() => expect(bar.getByRole("link", { name: source.artist.name }).getAttribute("href")).toBe(`/artists/${primary}`));
  expect(api.requestsTo("POST", "/api/v1/tracks/summaries")).toHaveLength(1);
  expect(api.requestsTo("POST", "/api/v1/tracks/summaries")[0]?.body).toEqual({ track_ids: [track.track_id] });
  await user.click(screen.getByRole("button", { name: "打开全屏播放器" }));
  const fullscreen = within(await screen.findByRole("dialog", { name: track.title }));
  expect(fullscreen.getByRole("link", { name: source.artist.name }).getAttribute("href")).toBe(`/artists/${primary}`);
  expect(audio.currentTime).toBe(37); expect(audio.paused).toBe(false);
  expect({ sources: audio.sources, load: audio.loadCalls, play: audio.playCalls, pause: audio.pauseCalls }).toEqual(before);
  expect(server.commands).toHaveLength(commands);
});
