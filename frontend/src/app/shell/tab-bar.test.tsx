import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { installFakeApi, installFakeSession, installOverviewApi } from "../../test/fake-api";
import { conversationOf, installStewardApi, settingsOf } from "../../test/fake-steward";
import { jsonResponse, sampleResponse } from "../../test/http";
import { readyCatalogState } from "../../test/search-data";
import { renderApp } from "../../test/render-app";
import { tabAt } from "./tab-bar";

describe("地址属于哪个标签", () => {
  it.each([
    ["/", "overview"],
    ["/albums", "albums"],
    ["/albums/00000000-0000-4000-8000-000000000001", "albums"],
    ["/artists", "artists"],
    ["/artists/00000000-0000-4000-8000-000000000002", "artists"],
    ["/steward", "steward"],
    ["/steward/00000000-0000-4000-8000-000000000004", "steward"],
    ["/me", "me"],
    ["/me/liked", "me"],
    ["/me/playlists/00000000-0000-4000-8000-000000000003", "me"],
    ["/settings/account", "me"],
    ["/issues", "me"],
    ["/claims", "me"],
    ["/artist-candidates", "me"],
    ["/search", null],
    // 前缀相同但不是子路径的地址不算。
    ["/albumsx", null],
    ["/meow", null],
  ] as const)("%s → %s", (pathname, tab) => {
    expect(tabAt(pathname)).toBe(tab);
  });
});

// jsdom 没有排版：用 matchMedia 替身表示窄屏，外壳据此渲染标签栏。
const originalMatchMedia = window.matchMedia;
let narrow = true;
beforeEach(() => {
  narrow = true;
  window.matchMedia = (query: string) => ({ ...originalMatchMedia(query), matches: narrow && query === "(max-width: 767px)" });
});
afterEach(() => {
  window.matchMedia = originalMatchMedia;
});

function setup(options: { artists?: boolean; steward?: "on" | "disabled" } = {}) {
  const api = installFakeApi();
  // 已有会话：标签栏进入时仍是空白新对话，不转到最近的会话。
  if (options.steward !== undefined) {
    installStewardApi(api, { settings: settingsOf({ display_name: "小蓝" }), disabled: options.steward === "disabled", conversations: [conversationOf("00000000-0000-4000-8000-000000000005")] });
  }
  installFakeSession(api, { current: "curator" });
  installOverviewApi(api);
  api.on("GET", "/api/v1/catalog/status", () => jsonResponse(200, options.artists === false ? { ...readyCatalogState(), artists_state: "unavailable" } : readyCatalogState()));
  api.on("GET", "/api/v1/me/playlists", () => sampleResponse("playlists/list"));
  return api;
}

/** 窄屏的“主导航”是标签栏（侧栏在 jsdom 里也渲染，取标签栏所在的那个）。 */
function bar(): HTMLElement {
  const navs = screen.getAllByRole("navigation", { name: "主导航" });
  const found = navs.find((nav) => nav.querySelector("a[href='/me']") !== null);
  if (found === undefined) throw new Error("没有标签栏");
  return found;
}

describe("窄屏底部标签栏", () => {
  it("概览、专辑、艺人、我的四项，当前标签 aria-current=page；切换后随之变化", async () => {
    setup();
    const { history } = renderApp({ path: "/" });
    await screen.findByRole("heading", { level: 1, name: "概览" });
    await waitFor(() => expect(within(bar()).getAllByRole("link").map((link) => link.textContent)).toEqual(["概览", "专辑", "艺人", "我的"]));
    const links = () => within(bar()).getAllByRole("link");
    // 当前标签由 aria-current 表达，不只靠颜色。
    expect(links().filter((link) => link.getAttribute("aria-current") === "page").map((link) => link.textContent)).toEqual(["概览"]);
    await userEvent.click(within(bar()).getByRole("link", { name: "我的" }));
    await screen.findByRole("heading", { level: 1, name: "我的" });
    expect(history.location.pathname).toBe("/me");
    expect(links().filter((link) => link.getAttribute("aria-current") === "page").map((link) => link.textContent)).toEqual(["我的"]);
  });

  it.each([
    ["/settings/account", "账号", "我的"],
    ["/me/history", "播放历史", "我的"],
  ])("%s 归在“%s”所在的标签“%s”下", async (path, heading, tab) => {
    setup();
    renderApp({ path });
    await screen.findByRole("heading", { level: 1, name: heading });
    await waitFor(() => expect(within(bar()).getAllByRole("link").filter((link) => link.getAttribute("aria-current") === "page").map((link) => link.textContent)).toEqual([tab]));
  });

  it("后端没有艺人能力时没有“艺人”标签", async () => {
    setup({ artists: false });
    renderApp({ path: "/" });
    await screen.findByRole("heading", { level: 1, name: "概览" });
    await waitFor(() => expect(within(bar()).getAllByRole("link").map((link) => link.textContent)).toEqual(["概览", "专辑", "我的"]));
  });

  it("宽屏不渲染标签栏", async () => {
    narrow = false;
    setup();
    renderApp({ path: "/" });
    await screen.findByRole("heading", { level: 1, name: "概览" });
    expect(screen.getAllByRole("navigation", { name: "主导航" })).toHaveLength(1);
    expect(screen.getByRole("navigation", { name: "主导航" }).querySelector("a[href='/me']")).toBeNull();
  });
  it("读到管家设置时第五项是管家（显示起的名字），在艺人与我的之间，进空白的新对话", async () => {
    setup({ steward: "on" });
    const { history } = renderApp({ path: "/" });
    await screen.findByRole("heading", { level: 1, name: "概览" });
    await waitFor(() => expect(within(bar()).getAllByRole("link").map((link) => link.textContent)).toEqual(["概览", "专辑", "艺人", "小蓝", "我的"]));
    await userEvent.click(within(bar()).getByRole("link", { name: "小蓝" }));
    await waitFor(() => expect(history.location.pathname).toBe("/steward"));
    await waitFor(() => expect(within(bar()).getByRole("link", { name: "小蓝" }).getAttribute("aria-current")).toBe("page"));
    expect(await screen.findByRole("textbox", { name: "问问小蓝" })).toBeTruthy();
    expect(history.location.pathname).toBe("/steward");
  });

  it("管理员关闭了成员使用管家时没有这一项", async () => {
    setup({ steward: "disabled" });
    renderApp({ path: "/" });
    await screen.findByRole("heading", { level: 1, name: "概览" });
    await waitFor(() => expect(within(bar()).getAllByRole("link").map((link) => link.textContent)).toEqual(["概览", "专辑", "艺人", "我的"]));
  });
});
