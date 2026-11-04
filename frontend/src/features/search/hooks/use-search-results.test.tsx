import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../../auth";
import { catalogStateKeys, type CatalogStateDTO } from "../../catalog";
import { deferred } from "../../../test/deferred";
import { installFakeApi, installFakeSession } from "../../../test/fake-api";
import { jsonResponse } from "../../../test/http";
import { readyCatalogState, searchAlbumPage, searchTrackItem, searchTrackPage } from "../../../test/search-data";
import { searchKeys } from "../query-keys";
import { useAlbumResults, useTrackResults } from "./use-search-results";

const cleanups: (() => void)[] = [];
afterEach(() => { for (const cleanup of cleanups.splice(0)) cleanup(); });
const legacy: CatalogStateDTO = { epoch: 1, search_mode: "legacy", artists_state: "unavailable" };
const normalized = readyCatalogState();

async function context(initial: CatalogStateDTO = legacy) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  const controller = createSessionController({ channel: null });
  await controller.start();
  const state = controller.getSnapshot();
  if (state.status !== "authenticated") throw new Error("应已登录");
  const { viewer, generation } = state;
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
  cleanups.push(() => { controller.dispose(); client.clear(); });
  const statusKey = catalogStateKeys.current(state.generation);
  client.setQueryData(statusKey, { status: 200, data: initial });
  function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}><SessionProvider controller={controller}>
      <ViewerScopeProvider viewer={viewer} generation={generation}>{children}</ViewerScopeProvider>
    </SessionProvider></QueryClientProvider>;
  }
  return { api, client, statusKey, generation: state.generation, Wrapper };
}

describe("搜索结果 epoch 隔离", () => {
  it.each(["albums", "tracks"] as const)("%s 响应从 legacy 发布到 normalized，迁入新键而不重复请求", async (type) => {
    const { api, client, statusKey, generation, Wrapper } = await context();
    const page = type === "albums" ? searchAlbumPage({ pagination: { page: 3, page_size: 12 } }) : searchTrackPage({ pagination: { page: 3, page_size: 20 } });
    api.on("GET", "/api/v1/search", () => jsonResponse(200, page));
    const hook = type === "albums" ? useAlbumResults : useTrackResults;
    const { result } = renderHook(() => hook("爱", 3), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.data?.catalog_state).toEqual(normalized));
    expect(client.getQueryData(statusKey)).toEqual({ status: 200, data: normalized });
    expect(client.getQueryData(searchKeys.page(generation, normalized.epoch, type, "爱", 3, type === "albums" ? 12 : 20))).toEqual({ status: 200, data: page });
    expect(api.requestsTo("GET", "/api/v1/search")).toHaveLength(1);
    expect(result.current.isPlaceholderData).toBe(false);
  });

  it("epoch 上升时不把旧结果当成新代次的翻页占位显示", async () => {
    const { api, client, statusKey, Wrapper } = await context();
    const oldItem = { ...searchTrackItem(), title: "旧规则命中" };
    api.on("GET", "/api/v1/search", () => jsonResponse(200, searchTrackPage({ catalog_state: legacy, items: [oldItem] })));
    const { result } = renderHook(() => useTrackResults("爱", 1), { wrapper: Wrapper });
    await waitFor(() => expect(result.current.data?.items[0]?.title).toBe("旧规则命中"));
    const pending = deferred<Response>();
    api.on("GET", "/api/v1/search", () => pending.promise);
    act(() => client.setQueryData(statusKey, { status: 200, data: normalized }));
    await waitFor(() => expect(api.requestsTo("GET", "/api/v1/search")).toHaveLength(2));
    // 必须在新响应尚未到达时检查，不能只验证最终页面碰巧变成新结果。
    const transitionalTitle = result.current.data?.items[0]?.title;
    await act(async () => pending.resolve(jsonResponse(200, searchTrackPage({ items: [{ ...oldItem, title: "新规则命中" }] }))));
    await waitFor(() => expect(result.current.data?.items[0]?.title).toBe("新规则命中"));
    expect(transitionalTitle).not.toBe("旧规则命中");
  });

  it("迟到的旧 epoch 响应不能回滚状态或覆盖新结果", async () => {
    const { api, client, statusKey, Wrapper } = await context();
    const pending = deferred<Response>();
    api.on("GET", "/api/v1/search", () => pending.promise);
    const { result } = renderHook(() => useTrackResults("爱", 2), { wrapper: Wrapper });
    await waitFor(() => expect(api.requestsTo("GET", "/api/v1/search")).toHaveLength(1));
    const oldRequest = api.requestsTo("GET", "/api/v1/search")[0];
    api.on("GET", "/api/v1/search", () => jsonResponse(200, searchTrackPage({ items: [{ ...searchTrackItem(), title: "新规则命中" }], pagination: { page: 2, page_size: 20 } })));
    act(() => client.setQueryData(statusKey, { status: 200, data: normalized }));
    await waitFor(() => expect(result.current.data?.items[0]?.title).toBe("新规则命中"));
    expect(oldRequest?.signal?.aborted).toBe(true);
    await act(async () => pending.resolve(jsonResponse(200, searchTrackPage({ catalog_state: legacy, items: [{ ...searchTrackItem(), title: "迟到旧规则" }] }))));
    expect(result.current.data?.items[0]?.title).toBe("新规则命中");
    expect(client.getQueryData(statusKey)).toEqual({ status: 200, data: normalized });
    expect(api.requestsTo("GET", "/api/v1/search")).toHaveLength(2);
  });

  it("同 epoch 翻页可保留上一页，新页数据随后替换", async () => {
    const { api, Wrapper } = await context(normalized);
    api.on("GET", "/api/v1/search", () => jsonResponse(200, searchTrackPage({ pagination: { page: 1, page_size: 20 } })));
    const { result, rerender } = renderHook(({ page }) => useTrackResults("爱", page), { initialProps: { page: 1 }, wrapper: Wrapper });
    await waitFor(() => expect(result.current.data?.pagination.page).toBe(1));
    const pending = deferred<Response>();
    api.on("GET", "/api/v1/search", () => pending.promise);
    rerender({ page: 2 });
    await waitFor(() => expect(result.current.isPlaceholderData).toBe(true));
    expect(result.current.data?.pagination.page).toBe(1);
    await act(async () => pending.resolve(jsonResponse(200, searchTrackPage({ pagination: { page: 2, page_size: 20 } }))));
    await waitFor(() => expect(result.current.data?.pagination.page).toBe(2));
    expect(result.current.isPlaceholderData).toBe(false);
  });
});
