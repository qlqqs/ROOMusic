import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { sampleBody } from "../../../test/api-samples";
import { deferred } from "../../../test/deferred";
import { installFakeApi } from "../../../test/fake-api";
import { jsonResponse, sampleResponse } from "../../../test/http";
import { readyCatalogState } from "../../../test/search-data";
import { createSessionController, SessionProvider, ViewerScopeProvider } from "../../auth";
import { catalogStateKeys } from "../../catalog";
import { decodeSearchSuggestions } from "../api/decoders";
import { searchKeys } from "../query-keys";
import { useSearchSuggestions } from "./use-search-suggestions";

const path = "/api/v1/search/suggestions";
const sample = decodeSearchSuggestions(sampleBody("search/suggestions"));
const disposers: (() => void)[] = [];

afterEach(() => { for (const dispose of disposers.splice(0)) dispose(); });

function setup(q: string | null, statusKnown = true, statusResponse?: Promise<Response>) {
  const api = installFakeApi();
  api.on("GET", "/api/v1/catalog/status", () => statusResponse ?? sampleResponse("search/catalog-status-ready"));
  api.on("GET", path, () => sampleResponse("search/suggestions"));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  const session = createSessionController({ channel: null });
  const generation = session.currentGeneration();
  if (statusKnown) client.setQueryData(catalogStateKeys.current(generation), { status: 200, data: readyCatalogState() });
  disposers.push(() => { client.clear(); session.dispose(); });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      <SessionProvider controller={session}>
        <ViewerScopeProvider viewer={{ id: "00000000-0000-4000-8000-000000000002", username: "curator", role: "admin" }} generation={generation}>
          {children}
        </ViewerScopeProvider>
      </SessionProvider>
    </QueryClientProvider>
  );
  const hook = renderHook(({ query }: { query: string | null }) => useSearchSuggestions(query), { initialProps: { query: q }, wrapper });
  return { ...hook, api, client, generation, requests: () => api.requestsTo("GET", path) };
}

describe("搜索建议查询", () => {
  it("null 不查询；单字爱使用完整会话/epoch/词/limit 查询键", async () => {
    const { result, rerender, requests, client, generation } = setup(null);
    expect(requests()).toHaveLength(0);
    rerender({ query: "爱" });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual(sample);
    expect(requests()).toHaveLength(1);
    const params = new URL(requests()[0]?.path ?? "", "http://localhost").searchParams;
    expect(Object.fromEntries(params)).toEqual({ q: "爱", limit: "5" });
    expect(client.getQueryData(searchKeys.suggestions(generation, readyCatalogState().epoch, "爱", 5))).toEqual({ status: 200, data: sample });
  });

  it("目录状态未返回时不抢发建议请求，状态就绪后自动查询", async () => {
    const status = deferred<Response>();
    const { result, api, requests } = setup("爱", false, status.promise);
    expect(api.requestsTo("GET", "/api/v1/catalog/status")).toHaveLength(1);
    expect(requests()).toHaveLength(0);
    expect(result.current.fetchStatus).toBe("idle");
    await act(async () => status.resolve(sampleResponse("search/catalog-status-ready")));
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(api.requestsTo("GET", "/api/v1/catalog/status")).toHaveLength(1);
    expect(requests()).toHaveLength(1);
  });

  it("换词取消旧请求，忽略取消信号的旧响应迟到也不覆盖新数据", async () => {
    const { api, result, rerender, requests } = setup(null);
    const old = deferred<Response>();
    api.on("GET", path, (request) => {
      const q = new URL(request.path, "http://localhost").searchParams.get("q");
      return q === "旧词" ? old.promise : jsonResponse(200, { ...sample, query: "爱" });
    });
    rerender({ query: "旧词" });
    await waitFor(() => expect(requests()).toHaveLength(1));
    const signal = requests()[0]?.signal;
    expect(signal?.aborted).toBe(false);
    rerender({ query: "爱" });
    await waitFor(() => expect(result.current.data?.query).toBe("爱"));
    expect(signal?.aborted).toBe(true);
    await act(async () => old.resolve(jsonResponse(200, { ...sample, query: "旧词", albums: { items: [], has_more: false }, tracks: { items: [], has_more: false } })));
    expect(result.current.data?.query).toBe("爱");
    expect(result.current.data?.tracks.items).toEqual(sample.tracks.items);
    expect(result.current.isPlaceholderData).toBe(false);
  });

  it("新词加载时旧建议只作占位，完成后切换为新词结果", async () => {
    const { api, result, rerender } = setup("爱");
    await waitFor(() => expect(result.current.data?.query).toBe("爱"));
    const next = deferred<Response>();
    api.on("GET", path, () => next.promise);
    rerender({ query: "新词" });
    expect(result.current.isPlaceholderData).toBe(true);
    expect(result.current.data?.query).toBe("爱");
    await act(async () => next.resolve(jsonResponse(200, { ...sample, query: "新词" })));
    await waitFor(() => expect(result.current.isPlaceholderData).toBe(false));
    expect(result.current.data?.query).toBe("新词");
  });

  it.each(["清空", "卸载"])("%s时中止正在进行的请求", async (action) => {
    const { api, rerender, unmount, requests } = setup(null);
    const pending = deferred<Response>();
    api.on("GET", path, () => pending.promise);
    rerender({ query: "爱" });
    await waitFor(() => expect(requests()).toHaveLength(1));
    const signal = requests()[0]?.signal;
    if (action === "清空") rerender({ query: null });
    else unmount();
    expect(signal?.aborted).toBe(true);
    await act(async () => pending.resolve(sampleResponse("search/suggestions")));
    expect(requests()).toHaveLength(1);
  });

  it("空结果是成功，错误响应是错误而不是伪造空数组", async () => {
    const { api, result, rerender } = setup(null);
    api.on("GET", path, () => sampleResponse("search/suggestions-empty"));
    rerender({ query: "空结果" });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.albums.items).toEqual([]);
    expect(result.current.data?.tracks.items).toEqual([]);
    api.on("GET", path, () => sampleResponse("errors/database-unavailable"));
    rerender({ query: "失败" });
    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.data).toBeUndefined();
    expect(result.current.error).not.toBeNull();
  });
});
