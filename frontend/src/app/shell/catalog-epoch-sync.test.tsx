import { act, cleanup, fireEvent, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { albumGroups } from "../../test/catalog-data";
import { FakeAudio } from "../../test/fake-audio";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { jsonResponse } from "../../test/http";
import { renderApp } from "../../test/render-app";
import { readyCatalogState, searchAlbumPage, searchTrackPage } from "../../test/search-data";
import type { CatalogStateDTO } from "../../features/catalog";

let audio: FakeAudio;
let visibility: DocumentVisibilityState;
beforeEach(() => {
  vi.useFakeTimers();
  visibility = "visible";
  vi.spyOn(document, "visibilityState", "get").mockImplementation(() => visibility);
  audio = new FakeAudio();
  vi.stubGlobal("Audio", class { constructor() { return audio; } });
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("probably");
  sessionStorage.clear();
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
async function tick(ms: number) { await act(async () => { await vi.advanceTimersByTimeAsync(ms); }); }
function setup() {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  let status: CatalogStateDTO = { epoch: 1, search_mode: "legacy", artists_state: "unavailable" };
  api.on("GET", "/api/v1/catalog/status", () => jsonResponse(200, status));
  return { api, publish: () => { status = readyCatalogState(); }, state: () => status,
    reads: () => api.requestsTo("GET", "/api/v1/catalog/status").length };
}

describe("外壳目录状态自动同步", () => {
  it("每三秒轮询、隐藏暂停、回前台重读，发布后停止轮询并刷新正在看的目录", async () => {
    const { api, publish, state, reads } = setup();
    api.on("GET", "/api/v1/release-groups", () => jsonResponse(200, {
      items: albumGroups(1, () => ({ title: state().search_mode === "legacy" ? "旧目录结果" : "新目录结果" })),
      pagination: { page: 1, page_size: 12, total: 1 },
    }));
    renderApp({ path: "/albums?q=爱" });
    await tick(100);
    expect(screen.getByRole("link", { name: /旧目录结果/ })).toBeTruthy();
    expect(reads()).toBe(1);
    await tick(2800);
    expect(reads()).toBe(1);
    await tick(200);
    expect(reads()).toBe(2);
    act(() => { visibility = "hidden"; document.dispatchEvent(new Event("visibilitychange", { bubbles: true })); });
    await tick(6000);
    expect(reads()).toBe(2);
    publish();
    act(() => { visibility = "visible"; document.dispatchEvent(new Event("visibilitychange", { bubbles: true })); });
    await tick(100);
    expect(reads()).toBe(3);
    expect(screen.getByRole("link", { name: /新目录结果/ })).toBeTruthy();
    expect(screen.queryByRole("link", { name: /旧目录结果/ })).toBeNull();
    expect(api.requestsTo("GET", "/api/v1/release-groups")).toHaveLength(2);
    await tick(9000);
    expect(reads()).toBe(3);
    act(() => { visibility = "hidden"; document.dispatchEvent(new Event("visibilitychange", { bubbles: true })); });
    act(() => { visibility = "visible"; document.dispatchEvent(new Event("visibilitychange", { bubbles: true })); });
    await tick(100);
    expect(reads()).toBe(4);
  });

  it("状态接口自然发布后保留搜索词、双栏页码及当前播放位置，不重新加载音频", async () => {
    const { api, publish, state } = setup();
    api.on("GET", "/api/v1/search", (request) => {
      const params = new URL(request.path, "http://test").searchParams;
      const options = { query: params.get("q") ?? "", catalog_state: state(), pagination: {
        page: Number(params.get("page")), page_size: Number(params.get("page_size")), total: 1000, total_relation: "gte" as const, has_more: true,
      } };
      return jsonResponse(200, params.get("type") === "albums" ? searchAlbumPage(options) : searchTrackPage(options));
    });
    api.on("GET", /\/tracks\/[^/]+\/playback$/, (request) => {
      const id = request.path.split("/").at(-2);
      return jsonResponse(200, { track_id: id, representation: { kind: "original", url: `/api/v1/tracks/${id}/audio`, mime_type: "audio/flac", codecs: null, size_bytes: 100 } });
    });
    const { history } = renderApp({ path: "/search?q=爱&albums_page=2&tracks_page=3" });
    await tick(100);
    const play = screen.getByRole("button", { name: /^播放 / });
    expect(play.hasAttribute("disabled")).toBe(false);
    fireEvent.click(play);
    await tick(100);
    act(() => { audio.loadMetadata(120); audio.beginPlaying(); audio.tick(17); });
    const before = { sources: [...audio.sources], loads: audio.loadCalls, plays: audio.playCalls };
    publish();
    await tick(3000);
    expect(api.requestsTo("GET", "/api/v1/search")).toHaveLength(4);
    expect(new URLSearchParams(history.location.search)).toEqual(new URLSearchParams({ q: "爱", albums_page: "2", tracks_page: "3" }));
    expect(audio.currentTime).toBe(17);
    expect(audio.paused).toBe(false);
    expect({ sources: audio.sources, loads: audio.loadCalls, plays: audio.playCalls }).toEqual(before);
  });
});
