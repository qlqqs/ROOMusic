import { act, cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { decodeRecentlyAdded } from "../features/catalog/api/decoders";
import { sampleBody } from "../test/api-samples";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { installCatalogApi } from "../test/fake-catalog";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const recentPath = "/api/v1/release-groups/recently-added";
const panel = () => screen.findByRole("region", { name: "最近新增" });

function setup(role: "admin" | "user" = "admin") {
  const api = installFakeApi();
  installFakeSession(api, { current: role === "admin" ? "curator" : "listener" });
  installOverviewApi(api);
  return api;
}

afterEach(() => {
  // 先卸载真实组件，让观察器清理仍在替身有效期间执行，再恢复所有全局对象。
  cleanup();
  vi.unstubAllGlobals();
});

/** 只替换浏览器布局边界，不替换组件、列数 hook 或查询 hook。 */
function controlGridColumns(initialColumns: number | null) {
  let columns = initialColumns;
  const originalGetComputedStyle = globalThis.getComputedStyle;
  const isRecentGrid = (element: Element) => element instanceof HTMLUListElement
    && element.closest("section")?.querySelector("h2")?.textContent === "最近新增";
  vi.stubGlobal("getComputedStyle", (element: Element, pseudoElement?: string | null) => {
    const style = originalGetComputedStyle(element, pseudoElement);
    if (isRecentGrid(element)) {
      Object.defineProperty(style, "gridTemplateColumns", {
        value: columns === null ? "" : Array.from({ length: columns }, () => "180px").join(" "),
      });
    }
    return style;
  });

  const observers: ControlledResizeObserver[] = [];
  class ControlledResizeObserver implements ResizeObserver {
    readonly targets = new Set<Element>();
    readonly disconnect = vi.fn(() => this.targets.clear());

    constructor(private readonly callback: ResizeObserverCallback) {
      observers.push(this);
    }

    observe(target: Element) {
      this.targets.add(target);
    }

    unobserve(target: Element) {
      this.targets.delete(target);
    }

    notify() {
      if ([...this.targets].some(isRecentGrid)) this.callback([], this);
    }
  }
  vi.stubGlobal("ResizeObserver", ControlledResizeObserver);

  return {
    resize(nextColumns: number | null) {
      columns = nextColumns;
      act(() => {
        for (const observer of observers) observer.notify();
      });
    },
    observerFor(element: Element) {
      const observer = observers.find((candidate) => candidate.targets.has(element));
      if (observer === undefined) throw new Error("最近新增网格没有被观察");
      return observer;
    },
  };
}

function expectRecentCards(region: HTMLElement, count: number) {
  const data = decodeRecentlyAdded(sampleBody("scan-feedback/recent-eight"));
  const list = within(region).getByRole("list");
  expect(within(list).getAllByRole("listitem")).toHaveLength(count);
  expect(within(list).getAllByRole("link").map((link) => link.getAttribute("href")))
    .toEqual(data.items.slice(0, count).map((item) => `/albums/${item.id}`));
  // 检查真实 DOM，而不只是可访问树；不能把多余卡片藏起来留在 Tab 顺序中。
  expect(list.querySelectorAll("li")).toHaveLength(count);
  for (const item of data.items.slice(count)) {
    expect(region.querySelector(`a[href="/albums/${item.id}"]`)).toBeNull();
    expect(within(region).queryByText(item.representative.title)).toBeNull();
  }
  expect(within(region).getByRole("link", { name: "查看全部" }).getAttribute("href"))
    .toBe("/albums?sort=added_at_desc");
}

describe("概览最近新增", () => {
  it("读取中保留占位，不伪装为空；返回空列表后整个区块隐藏", async () => {
    const api = setup();
    const pending = deferred<Response>();
    api.on("GET", recentPath, () => pending.promise);
    renderApp();
    const region = await panel();
    expect(within(region).getByRole("status").textContent).toContain("正在加载专辑");
    pending.resolve(sampleResponse("scan-feedback/recent-empty"));
    await waitFor(() => expect(screen.queryByRole("region", { name: "最近新增" })).toBeNull());
    expect(api.requestsTo("GET", recentPath)).toHaveLength(1);
  });

  it("旧服务的 404 隐藏区块，不重试或显示错误", async () => {
    const api = setup();
    api.on("GET", recentPath, () => sampleResponse("errors/not-found-api"));
    renderApp();
    await screen.findByRole("heading", { level: 1, name: "概览" });
    await waitFor(() => {
      expect(api.requestsTo("GET", recentPath)).toHaveLength(1);
      expect(screen.queryByRole("region", { name: "最近新增" })).toBeNull();
    });
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it.each(["admin", "user"] as const)("%s 在 jsdom 无法测量列数时显示八张卡片，保留服务端顺序与查看全部地址", async (role) => {
    const api = setup(role);
    api.on("GET", recentPath, () => sampleResponse("scan-feedback/recent-eight"));
    renderApp();
    const region = await panel();
    const data = decodeRecentlyAdded(sampleBody("scan-feedback/recent-eight"));
    await waitFor(() => expect(within(region).getAllByRole("link")).toHaveLength(9));
    const links = within(region).getAllByRole("link");
    expect(within(region).getByRole("link", { name: "查看全部" }).getAttribute("href")).toBe("/albums?sort=added_at_desc");
    expect(links.filter((link) => /\/albums\//.test(link.getAttribute("href") ?? "")).map((link) => link.getAttribute("href"))).toEqual(data.items.map((item) => `/albums/${item.id}`));
    if (role === "user") {
      expect(api.requestsTo("GET", "/api/v1/library-roots")).toHaveLength(0);
      expect(api.requestsTo("GET", /^\/api\/v1\/scans\/[^/]+\/report$/)).toHaveLength(0);
    }
  });

  it("读取失败显示错误和重试，不当作没有新增", async () => {
    const api = setup();
    api.on("GET", recentPath, () => sampleResponse("errors/database-unavailable"));
    const { runtime } = renderApp();
    // 本用例只验证手动重试，关闭自动重试以避免后台请求干扰。
    runtime.queryClient.setDefaultOptions({ queries: { retry: false } });
    const region = await panel();
    expect(await within(region).findByRole("alert")).toBeTruthy();
    expect(within(region).getByRole("button", { name: "重试" })).toBeTruthy();
    api.on("GET", recentPath, () => sampleResponse("scan-feedback/recent-one"));
    await userEvent.click(within(region).getByRole("button", { name: "重试" }));
    await waitFor(() => expect(within(region).getAllByRole("link")).toHaveLength(2));
    expect(within(region).queryByRole("alert")).toBeNull();
  });

  it("最近新增卡片进入详情，再返回列表时仍按最近新增排序", async () => {
    const api = setup("user");
    const data = decodeRecentlyAdded(sampleBody("scan-feedback/recent-one"));
    installCatalogApi(api, { groups: data.items });
    api.on("GET", recentPath, () => sampleResponse("scan-feedback/recent-one"));
    const { history } = renderApp();
    const region = await panel();
    await waitFor(() => expect(within(region).getAllByRole("link")).toHaveLength(2));
    const card = within(region).getAllByRole("link").find((link) => /\/albums\//.test(link.getAttribute("href") ?? ""));
    if (card === undefined) throw new Error("没有最近新增卡片");
    await userEvent.click(card);
    await userEvent.click(await screen.findByRole("link", { name: "返回专辑列表" }));
    await waitFor(() => expect(history.location.pathname).toBe("/albums"));
    expect(new URLSearchParams(history.location.search).get("sort")).toBe("added_at_desc");
  });
});


describe("概览最近新增：按实际网格列数截取", () => {
  it.each([
    { columns: 1, expected: 2 },
    { columns: 2, expected: 4 },
    { columns: 3, expected: 3 },
    { columns: 6, expected: 6 },
    { columns: 8, expected: 8 },
    { columns: 10, expected: 8 },
    { columns: null, expected: 8 },
  ])("$columns 列时占位与八条数据都显示 $expected 张，不渲染溢出卡片", async ({ columns, expected }) => {
    const layout = controlGridColumns(columns);
    const api = setup("user");
    const pending = deferred<Response>();
    api.on("GET", recentPath, () => pending.promise);
    const { view } = renderApp();
    const region = await panel();
    const loading = within(region).getByRole("status");
    expect(loading.textContent).toContain("正在加载专辑");
    const placeholders = within(loading).getByRole("list", { hidden: true });
    expect(placeholders.getAttribute("aria-hidden")).toBe("true");
    expect(within(placeholders).getAllByRole("listitem", { hidden: true })).toHaveLength(expected);
    expect(within(placeholders).queryByRole("link", { hidden: true })).toBeNull();
    const loadingObserver = layout.observerFor(placeholders);

    pending.resolve(sampleResponse("scan-feedback/recent-eight"));
    await waitFor(() => expect(within(region).queryByRole("status")).toBeNull());
    expectRecentCards(region, expected);
    expect(loadingObserver.disconnect).toHaveBeenCalledTimes(1);
    expect(loadingObserver.targets.size).toBe(0);
    const dataObserver = layout.observerFor(within(region).getByRole("list"));
    view.unmount();
    expect(dataObserver.disconnect).toHaveBeenCalledTimes(1);
    expect(dataObserver.targets.size).toBe(0);
    expect(api.requestsTo("GET", recentPath)).toHaveLength(1);
  });

  it("观察到列数变化时重新截取已有数据，扩宽恢复原顺序，不重新请求", async () => {
    const layout = controlGridColumns(6);
    const api = setup("user");
    api.on("GET", recentPath, () => sampleResponse("scan-feedback/recent-eight"));
    renderApp();
    const region = await panel();
    await waitFor(() => expect(within(region).queryByRole("status")).toBeNull());
    expectRecentCards(region, 6);
    const list = within(region).getByRole("list");
    layout.observerFor(list);

    for (const [columns, expected] of [[2, 4], [1, 2], [3, 3], [8, 8], [10, 8], [6, 6]] as const) {
      layout.resize(columns);
      expect(within(region).getByRole("list")).toBe(list);
      expectRecentCards(region, expected);
    }
    expect(api.requestsTo("GET", recentPath)).toHaveLength(1);
  });

  it("加载期间也随列数变化调整占位，数据到达沿用当前列数", async () => {
    const layout = controlGridColumns(6);
    const api = setup("user");
    const pending = deferred<Response>();
    api.on("GET", recentPath, () => pending.promise);
    renderApp();
    const region = await panel();
    const placeholders = within(region).getByRole("list", { hidden: true });
    const loadingObserver = layout.observerFor(placeholders);
    expect(placeholders.children).toHaveLength(6);

    layout.resize(2);
    expect(placeholders.children).toHaveLength(4);
    layout.resize(3);
    expect(placeholders.children).toHaveLength(3);

    pending.resolve(sampleResponse("scan-feedback/recent-eight"));
    await waitFor(() => expect(within(region).queryByRole("status")).toBeNull());
    expectRecentCards(region, 3);
    expect(loadingObserver.disconnect).toHaveBeenCalledTimes(1);
  });

  it.each([
    { columns: 1, placeholders: 2, total: 1 },
    { columns: 2, placeholders: 4, total: 3 },
    { columns: 3, placeholders: 3, total: 2 },
    { columns: 6, placeholders: 6, total: 5 },
    { columns: 8, placeholders: 8, total: 7 },
    { columns: 10, placeholders: 8, total: 7 },
    { columns: null, placeholders: 8, total: 7 },
  ])("$columns 列但只有 $total 条数据时，$placeholders 张占位缩为实际张数", async ({ columns, placeholders, total }) => {
    controlGridColumns(columns);
    const api = setup("user");
    const pending = deferred<Response>();
    api.on("GET", recentPath, () => pending.promise);
    renderApp();
    const region = await panel();
    expect(within(region).getByRole("list", { hidden: true }).children).toHaveLength(placeholders);
    const data = decodeRecentlyAdded(sampleBody("scan-feedback/recent-eight"));
    pending.resolve(jsonResponse(200, { items: data.items.slice(0, total) }));
    await waitFor(() => expect(within(region).queryByRole("status")).toBeNull());
    expectRecentCards(region, total);
  });
});
