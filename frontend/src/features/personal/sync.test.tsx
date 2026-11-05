import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { deferred } from "../../test/deferred";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { jsonResponse, sampleResponse } from "../../test/http";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../auth";
import type { FavoriteLookupDTO, FavoriteToggleDTO } from "./api/decoders";
import { FavoriteButton } from "./components/favorite-button";
import { FavoriteStatusScope } from "./components/favorite-status-scope";
import { usePlaylists } from "./hooks/use-playlists";
import { personalKeys } from "./query-keys";
import { PersonalSync } from "./sync";

const trackId = "00000000-0000-4000-8000-0000000000a1";
const target = { kind: "track", id: trackId, title: "同一首曲目" } as const;
const lookupPath = "/api/v1/me/favorites/lookup";
const targetPath = `/api/v1/me/favorites/tracks/${trackId}`;
const playlistsPath = "/api/v1/me/playlists";
const dispose: (() => void)[] = [];

afterEach(() => {
  cleanup();
  for (const release of dispose.splice(0)) release();
  vi.restoreAllMocks();
});

/** 仅代替浏览器传输：同名通道的其他连接收到消息，发送者不回收自己的广播。 */
function installBroadcastChannel() {
  const connections = new Set<MemoryChannel>();
  const messages: { channel: string; data: unknown }[] = [];
  class MemoryChannel {
    readonly name: string;
    closed = false;
    onmessage: ((event: MessageEvent<unknown>) => void) | null = null;

    constructor(name: string) {
      this.name = name;
      connections.add(this);
    }

    postMessage(data: unknown) {
      if (this.closed) throw new Error("测试通道已关闭");
      const payload: unknown = structuredClone(data);
      messages.push({ channel: this.name, data: payload });
      for (const connection of connections) {
        if (connection === this || connection.closed || connection.name !== this.name) continue;
        queueMicrotask(() => {
          if (!connection.closed) connection.onmessage?.(new MessageEvent<unknown>("message", { data: structuredClone(payload) }));
        });
      }
    }

    close() { this.closed = true; }
  }
  vi.stubGlobal("BroadcastChannel", MemoryChannel);
  dispose.push(() => { for (const connection of connections) connection.close(); });
  return { connections, messages, connect: (name = "roomusic:personal") => new MemoryChannel(name) };
}

function PlaylistCount() {
  const playlists = usePlaylists(1);
  return <output aria-label="歌单数量">{playlists.data?.pagination.total ?? "读取中"}</output>;
}

/** 每个窗口用独立的真实会话与 QueryClient，避免把同缓存同步误当成跨窗口同步。 */
function setup() {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  let favorited = false;
  let revision = 0;
  let playlistSample = "playlists/list-empty";
  const setServerFavorite = (next: boolean) => {
    if (favorited !== next) revision++;
    favorited = next;
  };
  const lookup = () => {
    const body: FavoriteLookupDTO = { revision, tracks: [{ track_id: trackId, favorited }], albums: [], artists: [] };
    return jsonResponse(200, body);
  };
  const commit = (next: boolean) => {
    setServerFavorite(next);
    const body: FavoriteToggleDTO = {
      target_kind: "track", target_id: trackId, favorited,
      created_at: favorited ? "2026-11-04T00:00:00Z" : null, revision,
    };
    return jsonResponse(200, body);
  };
  api.on("POST", lookupPath, (request) => {
    expect(request.body).toEqual({ track_ids: [trackId], group_ids: [] });
    return lookup();
  });
  api.on("PUT", targetPath, () => commit(true));
  api.on("DELETE", targetPath, () => commit(false));
  api.on("GET", playlistsPath, () => sampleResponse(playlistSample));
  const openWindow = async (name: string, includePlaylists = false) => {
    const controller = createSessionController({ channel: null });
    await controller.start();
    const state = controller.getSnapshot();
    if (state.status !== "authenticated") throw new Error("测试会话未就绪");
    // 禁用 Query 自带的焦点刷新：本文件只证明 PersonalSync 显式失效在新鲜期内也有效。
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000, refetchOnWindowFocus: false } } });
    dispose.push(() => { client.clear(); controller.dispose(); });
    const view = render(<QueryClientProvider client={client}>
      <SessionProvider controller={controller}>
        <ViewerScopeProvider viewer={state.viewer} generation={state.generation}>
          <PersonalSync>
            <section aria-label={name}>
              <FavoriteStatusScope trackIds={[trackId]}><FavoriteButton target={target} /></FavoriteStatusScope>
              {includePlaylists ? <PlaylistCount /> : null}
            </section>
          </PersonalSync>
        </ViewerScopeProvider>
      </SessionProvider>
    </QueryClientProvider>);
    const region = within(screen.getByRole("region", { name }));
    const button = region.getByRole("button", { name: `收藏《${target.title}》` });
    await waitFor(() => {
      expect(button.hasAttribute("disabled")).toBe(false);
      if (includePlaylists) expect(region.getByLabelText("歌单数量").textContent).toBe("0");
    });
    return { client, generation: state.generation, region, button, unmount: view.unmount };
  };
  return { api, commit, lookup, openWindow, setServerFavorite, setPlaylistSample: (sample: string) => { playlistSample = sample; }, user: userEvent.setup() };
}

async function settled(client: QueryClient) {
  await waitFor(() => {
    expect(client.isFetching()).toBe(0);
    expect(client.isMutating()).toBe(0);
  });
}

describe("个人音乐的跨窗口与聚焦同步", () => {
  it("心形成功后只广播范围，无个人载荷；另一窗口独立重读，接收者不回传", async () => {
    const channel = installBroadcastChannel();
    const { api, commit, openWindow, user } = setup();
    const first = await openWindow("窗口一");
    const second = await openWindow("窗口二");
    expect(first.client).not.toBe(second.client);
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(2);
    const response = deferred<Response>();
    api.on("PUT", targetPath, () => response.promise);
    await user.click(first.button);
    await waitFor(() => expect(first.button.getAttribute("aria-pressed")).toBe("true"));
    expect(second.button.getAttribute("aria-pressed")).toBe("false");
    expect(channel.messages).toEqual([]);
    await act(async () => response.resolve(commit(true)));
    await waitFor(() => expect(second.button.getAttribute("aria-pressed")).toBe("true"));
    await settled(first.client);
    await settled(second.client);
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(4);
    expect(channel.messages).toEqual([{ channel: "roomusic:personal", data: { scope: "favorites" } }]);
    await user.click(second.button);
    await waitFor(() => expect(first.button.getAttribute("aria-pressed")).toBe("false"));
    await settled(first.client);
    await settled(second.client);
    expect(channel.messages).toEqual([
      { channel: "roomusic:personal", data: { scope: "favorites" } },
      { channel: "roomusic:personal", data: { scope: "favorites" } },
    ]);
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(6);
  });

  it("明确失败不会广播未提交的收藏，也不会刷新另一窗口", async () => {
    const channel = installBroadcastChannel();
    const { api, openWindow, user } = setup();
    const first = await openWindow("窗口一");
    const second = await openWindow("窗口二");
    api.on("PUT", targetPath, () => sampleResponse("errors/personal-origin-forbidden"));
    await user.click(first.button);
    expect(await first.region.findByText("没能收藏，请稍后再试。")).toBeTruthy();
    await settled(first.client);
    await settled(second.client);
    expect(first.button.getAttribute("aria-pressed")).toBe("false");
    expect(second.button.getAttribute("aria-pressed")).toBe("false");
    expect(channel.messages).toEqual([]);
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(3);
  });

  it.each([
    { name: "支持广播", supported: true },
    { name: "不支持广播", supported: false },
  ])("$name 时窗口聚焦都重新读取新鲜缓存中的收藏与歌单", async ({ supported }) => {
    const channel = supported ? installBroadcastChannel() : null;
    if (!supported) vi.stubGlobal("BroadcastChannel", undefined);
    const { api, openWindow, setServerFavorite, setPlaylistSample } = setup();
    const window = await openWindow("窗口", true);
    setServerFavorite(true);
    setPlaylistSample("playlists/list");
    await act(async () => { globalThis.window.dispatchEvent(new Event("focus")); });
    await waitFor(() => {
      expect(window.button.getAttribute("aria-pressed")).toBe("true");
      expect(window.region.getByLabelText("歌单数量").textContent).toBe("2");
    });
    await settled(window.client);
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(2);
    expect(api.requestsTo("GET", playlistsPath)).toHaveLength(2);
    if (channel !== null) expect(channel.messages).toEqual([]);
  });

  it("页面隐藏不刷新，重新可见时不受三十秒新鲜期限制", async () => {
    const channel = installBroadcastChannel();
    const { api, openWindow, setServerFavorite, setPlaylistSample } = setup();
    const window = await openWindow("窗口", true);
    const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    setServerFavorite(true);
    setPlaylistSample("playlists/list");
    await act(async () => { document.dispatchEvent(new Event("visibilitychange")); });
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(1);
    expect(api.requestsTo("GET", playlistsPath)).toHaveLength(1);
    expect(window.button.getAttribute("aria-pressed")).toBe("false");
    visibility.mockReturnValue("visible");
    await act(async () => { document.dispatchEvent(new Event("visibilitychange")); });
    await waitFor(() => {
      expect(window.button.getAttribute("aria-pressed")).toBe("true");
      expect(window.region.getByLabelText("歌单数量").textContent).toBe("2");
    });
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(2);
    expect(api.requestsTo("GET", playlistsPath)).toHaveLength(2);
    expect(channel.messages).toEqual([]);
  });

  it("收到收藏或歌单通知只刷新对应范围，旧会话的缓存不失效", async () => {
    const channel = installBroadcastChannel();
    const { api, openWindow, setServerFavorite, setPlaylistSample } = setup();
    const window = await openWindow("窗口", true);
    const currentKey = personalKeys.favoriteLookup(window.generation, { trackIds: [trackId], groupIds: [] });
    const oldKey = personalKeys.favoriteLookup(window.generation - 1, { trackIds: [trackId], groupIds: [] });
    window.client.setQueryData(oldKey, window.client.getQueryData(currentKey));
    const peer = channel.connect();
    setServerFavorite(true);
    setPlaylistSample("playlists/list");
    await act(async () => peer.postMessage({ scope: "favorites" }));
    await waitFor(() => expect(window.button.getAttribute("aria-pressed")).toBe("true"));
    await settled(window.client);
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(2);
    expect(api.requestsTo("GET", playlistsPath)).toHaveLength(1);
    expect(window.region.getByLabelText("歌单数量").textContent).toBe("0");
    expect(window.client.getQueryState(oldKey)?.isInvalidated).toBe(false);
    await act(async () => peer.postMessage({ scope: "playlists" }));
    await waitFor(() => expect(window.region.getByLabelText("歌单数量").textContent).toBe("2"));
    await settled(window.client);
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(2);
    expect(api.requestsTo("GET", playlistsPath)).toHaveLength(2);
    expect(channel.messages).toHaveLength(2);
  });

  it("忽略损坏通知、未知范围和不同名通道，不把它们当作收藏数据", async () => {
    const channel = installBroadcastChannel();
    const { api, openWindow, setServerFavorite } = setup();
    const window = await openWindow("窗口");
    const peer = channel.connect();
    const unrelated = channel.connect("别的通道");
    setServerFavorite(true);
    await act(async () => {
      for (const payload of [null, "favorites", [], {}, { scope: "unknown" }, { scope: 123 }]) peer.postMessage(payload);
      unrelated.postMessage({ scope: "favorites" });
    });
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(1);
    expect(window.button.getAttribute("aria-pressed")).toBe("false");
    await act(async () => peer.postMessage({ scope: "favorites" }));
    await waitFor(() => expect(window.button.getAttribute("aria-pressed")).toBe("true"));
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(2);
    expect(channel.messages).toHaveLength(8);
  });

  it("卸载后关闭通道并移除聚焦和可见性监听，不再使缓存失效", async () => {
    const channel = installBroadcastChannel();
    const { api, openWindow, setServerFavorite } = setup();
    const window = await openWindow("窗口");
    const key = personalKeys.favoriteLookup(window.generation, { trackIds: [trackId], groupIds: [] });
    await settled(window.client);
    window.unmount();
    expect([...channel.connections]).toHaveLength(1);
    expect([...channel.connections].every((connection) => connection.closed)).toBe(true);
    const peer = channel.connect();
    setServerFavorite(true);
    vi.spyOn(document, "visibilityState", "get").mockReturnValue("visible");
    await act(async () => {
      peer.postMessage({ scope: "favorites" });
      globalThis.window.dispatchEvent(new Event("focus"));
      document.dispatchEvent(new Event("visibilitychange"));
    });
    expect(window.client.getQueryState(key)?.isInvalidated).toBe(false);
    expect(api.requestsTo("POST", lookupPath)).toHaveLength(1);
  });
});
