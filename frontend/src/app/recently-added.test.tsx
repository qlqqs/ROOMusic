import { cleanup, screen, waitFor, within } from "@testing-library/react";
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

function expectRecentCards(region: HTMLElement, count: number) {
  const data = decodeRecentlyAdded(sampleBody("scan-feedback/recent-eight"));
  const list = within(region).getByRole("list");
  expect(within(list).getAllByRole("listitem")).toHaveLength(count);
  expect(within(list).getAllByRole("link").map((link) => link.getAttribute("href")))
    .toEqual(data.items.slice(0, count).map((item) => `/albums/${item.id}`));
  expect(within(region).getByRole("link", { name: "全部专辑" }).getAttribute("href"))
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

  it.each(["admin", "user"] as const)("%s 一行横排显示全部八张卡片，保留服务端顺序与“全部专辑”地址", async (role) => {
    const api = setup(role);
    api.on("GET", recentPath, () => sampleResponse("scan-feedback/recent-eight"));
    renderApp();
    const region = await panel();
    const data = decodeRecentlyAdded(sampleBody("scan-feedback/recent-eight"));
    await waitFor(() => expect(within(region).getAllByRole("link")).toHaveLength(9));
    const links = within(region).getAllByRole("link");
    expect(within(region).getByRole("link", { name: "全部专辑" }).getAttribute("href")).toBe("/albums?sort=added_at_desc");
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

  it("横排不按列数截取：占位是八张，数据到达后接口给几张就显示几张", async () => {
    const api = setup("user");
    const pending = deferred<Response>();
    api.on("GET", recentPath, () => pending.promise);
    renderApp();
    const region = await panel();
    const placeholders = within(within(region).getByRole("status")).getByRole("list", { hidden: true });
    expect(placeholders.getAttribute("aria-hidden")).toBe("true");
    expect(within(placeholders).getAllByRole("listitem", { hidden: true })).toHaveLength(8);
    const data = decodeRecentlyAdded(sampleBody("scan-feedback/recent-eight"));
    pending.resolve(jsonResponse(200, { items: data.items.slice(0, 5) }));
    await waitFor(() => expect(within(region).queryByRole("status")).toBeNull());
    expectRecentCards(region, 5);
  });

  it("八张全部显示；jsdom 量不出溢出时不出现翻页按钮", async () => {
    const api = setup("user");
    api.on("GET", recentPath, () => sampleResponse("scan-feedback/recent-eight"));
    renderApp();
    const region = await panel();
    await waitFor(() => expect(within(region).queryByRole("status")).toBeNull());
    expectRecentCards(region, 8);
    expect(within(region).queryByRole("button", { name: /^最近新增：/ })).toBeNull();
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

