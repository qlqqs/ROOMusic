import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createEvent, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { artistDataOf, primaryArtistId, respondToArtistFavorites } from "../../test/artist-data";
import { installFakeApi, installFakeSession } from "../../test/fake-api";
import { installArtistFavoritesApi } from "../../test/fake-artists";
import { installPersonalApi } from "../../test/fake-personal";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../auth";
import { FavoriteButton } from "./components/favorite-button";
import { FavoriteStatusScope } from "./components/favorite-status-scope";

const dispose: (() => void)[] = [];
afterEach(() => { for (const cleanup of dispose.splice(0)) cleanup(); });
const id = primaryArtistId();
const other = "00000000-0000-4000-8000-000000000002";
const trackId = "00000000-0000-4000-8000-000000000004";
async function setup() {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: "listener" });
  const personal = installPersonalApi(api, { session });
  const data = artistDataOf();
  installArtistFavoritesApi(api, data, session, personal);
  const controller = createSessionController({ channel: null });
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("会话未就绪");
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
  dispose.push(() => { client.clear(); controller.dispose(); });
  const view = (children: ReactNode) => render(<QueryClientProvider client={client}><SessionProvider controller={controller}><ViewerScopeProvider viewer={state.viewer} generation={state.generation}>{children}</ViewerScopeProvider></SessionProvider></QueryClientProvider>);
  const favorite = (artistId: string) => respondToArtistFavorites(data, "PUT", new URL(`/api/v1/me/favorites/artists/${artistId}`, "http://test"), null, state.viewer.id);
  return { api, data, view, favorite };
}
const heart = (title: string) => screen.getByRole("button", { name: `收藏“${title}”` });
const hover = (element: HTMLElement) => {
  const event = createEvent.pointerOver(element);
  Object.defineProperty(event, "pointerType", { value: "mouse" });
  fireEvent(element, event);
};

describe("艺人收藏独立lookup", () => {
  it("I7：艺人收藏名称用引号，曲目和专辑收藏名称保留书名号", async () => {
    const { view } = await setup();
    view(<FavoriteStatusScope trackIds={[trackId]} groupIds={[other]} artistIds={[id]}>
      <FavoriteButton target={{ kind: "artist", id, title: "邓丽君" }} />
      <FavoriteButton target={{ kind: "track", id: trackId, title: "海边夜曲" }} />
      <FavoriteButton target={{ kind: "album", id: other, title: "夜曲集" }} />
    </FavoriteStatusScope>);
    await waitFor(() => expect(heart("邓丽君").hasAttribute("disabled")).toBe(false));
    expect(screen.queryByRole("button", { name: "收藏《邓丽君》" })).toBeNull();
    expect(screen.getByRole("button", { name: "收藏《海边夜曲》" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "收藏《夜曲集》" })).toBeTruthy();
  });

  it("混合范围发两次请求，艺人整理503不连累曲目心形", async () => {
    const { api, data, view } = await setup();
    data.state = "preparing";
    view(<FavoriteStatusScope trackIds={[trackId]} artistIds={[id]}><FavoriteButton target={{ kind: "track", id: trackId, title: "曲目" }} /><FavoriteButton target={{ kind: "artist", id, title: "艺人" }} /></FavoriteStatusScope>);
    await waitFor(() => expect(screen.getByRole("button", { name: "收藏《曲目》" }).hasAttribute("disabled")).toBe(false));
    hover(heart("艺人"));
    await waitFor(() => expect(screen.getByText("暂时读不到收藏状态").getAttribute("aria-hidden")).toBe("true"));
    expect(heart("艺人").hasAttribute("disabled")).toBe(true);
    const bodies = api.requestsTo("POST", "/api/v1/me/favorites/lookup").map((request) => request.body);
    expect(bodies).toContainEqual({ track_ids: [trackId], group_ids: [] });
    expect(bodies).toContainEqual({ track_ids: [], group_ids: [], artist_ids: [id] });
  });

  it("旧ID的lookup同时满足resolved新ID心形，取消后两处一起更新", async () => {
    const { data, favorite, view, api } = await setup();
    const source = data.details.get(other);
    if (!source) throw new Error("缺少来源");
    source.redirect_artist_id = id;
    favorite(other);
    view(<FavoriteStatusScope artistIds={[other]}><FavoriteButton target={{ kind: "artist", id: other, title: "旧身份" }} /><FavoriteButton target={{ kind: "artist", id, title: "现身份" }} /></FavoriteStatusScope>);
    await waitFor(() => expect(heart("旧身份").getAttribute("aria-pressed")).toBe("true"));
    expect(heart("现身份").getAttribute("aria-pressed")).toBe("true");
    await userEvent.click(heart("现身份"));
    await waitFor(() => expect(heart("旧身份").getAttribute("aria-pressed")).toBe("false"));
    expect(heart("现身份").getAttribute("aria-pressed")).toBe("false");
    expect(api.requestsTo("DELETE", `/api/v1/me/favorites/artists/${id}`)).toHaveLength(1);
  });

  it("切换时409 not_ready在心形旁提示整理且回滚，不说数据库故障", async () => {
    const { data, view } = await setup();
    view(<FavoriteStatusScope artistIds={[id]}><FavoriteButton target={{ kind: "artist", id, title: "艺人" }} /></FavoriteStatusScope>);
    await waitFor(() => expect(heart("艺人").hasAttribute("disabled")).toBe(false));
    data.state = "preparing";
    await userEvent.click(heart("艺人"));
    expect(await screen.findByText("艺人信息正在整理，请稍后再试。")).toBeTruthy();
    expect(heart("艺人").getAttribute("aria-pressed")).toBe("false");
    expect(screen.queryByText(/数据库/)).toBeNull();
  });
});
