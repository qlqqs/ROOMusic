import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { sampleBody } from "../test/api-samples";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { jsonResponse, sampleResponse } from "../test/http";
import { readyCatalogState } from "../test/search-data";
import { renderApp } from "../test/render-app";

const playlistsPath = "/api/v1/me/playlists";

function setup(options: { role?: "admin" | "user"; playlists?: () => Response } = {}) {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: options.role === "user" ? "listener" : "curator" });
  installOverviewApi(api);
  api.on("GET", "/api/v1/catalog/status", () => jsonResponse(200, readyCatalogState()));
  api.on("GET", playlistsPath, options.playlists ?? (() => sampleResponse("playlists/list")));
  return { api, session };
}

const main = () => within(screen.getByRole("main"));
const section = (name: string) => within(main().getByRole("region", { name }));
const linkNames = (scope: ReturnType<typeof section>) => scope.getAllByRole("link").map((link) => link.textContent);

describe("“我的”页（/me）", () => {
  it("/me 不再转到“我喜欢的音乐”，而是渲染“我的”页：账号、我的音乐、歌单、管理、设置与退出登录", async () => {
    setup();
    const { history } = renderApp({ path: "/me" });
    await screen.findByRole("heading", { level: 1, name: "我的" });
    expect(history.location.pathname).toBe("/me");
    expect(document.title).toBe("我的 · ROOMusic");
    // 账号一行：头像首字、用户名、角色，整行进账号设置。
    const account = main().getByRole("link", { name: "curator 管理员，账号设置" });
    expect(account.getAttribute("href")).toBe("/settings/account");
    expect(within(account).getByText("C").getAttribute("aria-hidden")).toBe("true");
    // 我的音乐：与侧栏同源，歌单另成一节。
    expect(linkNames(section("我的音乐"))).toEqual(["我喜欢的音乐", "我的收藏", "播放历史"]);
    const playlists = section("我的歌单");
    await waitFor(() => expect(playlists.getAllByRole("link").map((link) => link.textContent)).toEqual(["周末聆听2 首", "晚间聆听0 首"]));
    expect(playlists.getByRole("button", { name: "新建歌单" })).toBeTruthy();
    // 管理员的“管理”分组：与侧栏同样的三项（目录状态就绪，有艺人能力）。
    await waitFor(() => expect(linkNames(section("管理"))).toEqual(["元数据", "待认领", "重复艺人"]));
    const settings = section("设置与退出");
    expect(settings.getByRole("link", { name: "设置" }).getAttribute("href")).toBe("/settings");
    expect(settings.getByRole("button", { name: "退出登录" })).toBeTruthy();
  });

  it("普通成员没有“管理”分组，账号行写“成员”", async () => {
    setup({ role: "user" });
    renderApp({ path: "/me" });
    await screen.findByRole("heading", { level: 1, name: "我的" });
    expect(main().getByRole("link", { name: /^listener .+，账号设置$/ })).toBeTruthy();
    expect(main().queryByRole("region", { name: "管理" })).toBeNull();
    expect(main().queryByRole("link", { name: "元数据" })).toBeNull();
    expect(linkNames(section("我的音乐"))).toEqual(["我喜欢的音乐", "我的收藏", "播放历史"]);
  });

  it("歌单没有列全时末尾是“全部歌单”", async () => {
    const list = sampleBody("playlists/list") as { items: unknown[]; pagination: Record<string, number> };
    setup({ playlists: () => jsonResponse(200, { ...list, pagination: { ...list.pagination, total: 25 } }) });
    renderApp({ path: "/me" });
    await screen.findByRole("heading", { level: 1, name: "我的" });
    const all = await section("我的歌单").findByRole("link", { name: "全部歌单" });
    expect(all.getAttribute("href")).toBe("/me/playlists");
  });

  it("没有歌单时写一句说明，不列“全部歌单”", async () => {
    setup({ playlists: () => sampleResponse("playlists/list-empty") });
    renderApp({ path: "/me" });
    await screen.findByRole("heading", { level: 1, name: "我的" });
    expect(await section("我的歌单").findByText("还没有歌单，点“＋”新建一个。")).toBeTruthy();
    expect(section("我的歌单").queryByRole("link")).toBeNull();
  });

  it("点“我的音乐”里的入口进入对应页面", async () => {
    setup();
    const { history } = renderApp({ path: "/me" });
    await screen.findByRole("heading", { level: 1, name: "我的" });
    await userEvent.click(section("我的音乐").getByRole("link", { name: "播放历史" }));
    await waitFor(() => expect(history.location.pathname).toBe("/me/history"));
  });

  it("退出进行中显示“正在退出…”；失败时就地提示并可重试，重试成功才去登录页", async () => {
    const { api, session } = setup();
    const { history } = renderApp({ path: "/me" });
    await screen.findByRole("heading", { level: 1, name: "我的" });
    const gate = deferred<Response>();
    api.on("POST", "/api/v1/auth/logout", () => gate.promise);
    await userEvent.click(section("设置与退出").getByRole("button", { name: "退出登录" }));
    const busy = await section("设置与退出").findByRole("button", { name: "正在退出…" });
    expect(busy.getAttribute("aria-busy")).toBe("true");
    expect(main().getByRole("link", { name: "curator 正在退出…，账号设置" })).toBeTruthy();
    gate.resolve(sampleResponse("errors/database-unavailable"));
    const alert = await section("设置与退出").findByRole("alert");
    expect(alert.textContent).toContain("退出失败");
    expect(history.location.pathname).toBe("/me");
    expect(session.current).toBe("curator");
    expect(section("设置与退出").getByRole("button", { name: "退出登录" })).toBeTruthy();
    api.on("POST", "/api/v1/auth/logout", () => {
      session.current = null;
      return sampleResponse("auth/logout");
    });
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    await screen.findByRole("heading", { level: 1, name: "登录" });
    expect(history.location.pathname).toBe("/login");
    expect(api.requestsTo("POST", "/api/v1/auth/logout")).toHaveLength(2);
  });
});
