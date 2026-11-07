import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { renderApp } from "../test/render-app";

const scanId = "00000000-0000-4000-8000-000000000001";
// 重复键会被路由合并为数组，不在保证范围内。
const legacyAddresses = [
  ["/library?scan_page=2&extra=a%20b#history", "/settings/library?scan_page=2&extra=a%20b#history"],
  [`/library/scans/${scanId}?tab=report&extra=a%2Fb#report`, `/settings/library/scans/${scanId}?tab=report&extra=a%2Fb#report`],
  ["/members?tab=1&extra=a%20b#listener", "/settings/members?tab=1&extra=a%20b#listener"],
  [`/operations?page=2&release=${scanId}&group=${scanId}&artist=${scanId}&extra=a%2Fb#records`, `/settings/operations?page=2&release=${scanId}&group=${scanId}&artist=${scanId}&extra=a%2Fb#records`],
  ["/system/backups?tab=files&extra=a%20b#files", "/settings/backups?tab=files&extra=a%20b#files"],
] as const;

const managementPaths = ["/settings/library", `/settings/library/scans/${scanId}`, "/settings/members", "/settings/operations", "/settings/backups"];
const managementApis = ["/api/v1/users", "/api/v1/library-roots", "/api/v1/library-root-operations", "/api/v1/scans", "/api/v1/scans/active", `/api/v1/scans/${scanId}`, `/api/v1/scans/${scanId}/report`, "/api/v1/operations", "/api/v1/system/version", "/api/v1/system/backups"];

function setup(current: string | null = "curator") {
  const api = installFakeApi();
  installFakeSession(api, { current });
  return api;
}

const accountHeading = () => screen.findByRole("heading", { level: 1, name: "账号" });

/** 路由可规范化查询编码，但路径、hash 与单值参数顺序不能改变。 */
function expectAddress(actual: string, expected: string) {
  const actualUrl = new URL(actual, "http://localhost");
  const expectedUrl = new URL(expected, "http://localhost");
  expect(actualUrl.pathname).toBe(expectedUrl.pathname);
  expect(actualUrl.hash).toBe(expectedUrl.hash);
  expect(Array.from(actualUrl.searchParams.entries())).toEqual(Array.from(expectedUrl.searchParams.entries()));
}

describe("设置页与权限", () => {
  it("管理员有账号、扫描目录、成员、操作记录、备份五栏，账号展示用户名、角色与修改密码", async () => {
    setup();
    renderApp({ path: "/settings/account" });
    await accountHeading();
    const links = within(screen.getByRole("navigation", { name: "设置" })).getAllByRole("link");
    expect(links.map((link) => [link.textContent, link.getAttribute("href")])).toEqual([
      ["账号", "/settings/account"], ["扫描目录", "/settings/library"], ["成员", "/settings/members"], ["操作记录", "/settings/operations"], ["备份", "/settings/backups"],
    ]);
    expect(links[0]?.getAttribute("aria-current")).toBe("page");
    const main = within(screen.getByRole("main"));
    expect(main.getByText("curator")).toBeTruthy();
    expect(main.getByText("管理员")).toBeTruthy();
    expect(main.getByRole("button", { name: "修改密码" })).toBeTruthy();
    expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
    expect(document.title).toBe("账号 · ROOMusic");
    const nav = within(screen.getByRole("navigation", { name: "主导航" }));
    for (const name of ["扫描目录", "成员", "操作记录", "备份"]) expect(nav.queryByRole("link", { name })).toBeNull();
    expect(nav.queryByText("系统")).toBeNull();
    for (const name of ["元数据", "待认领", "重复艺人"]) expect(await nav.findByRole("link", { name })).toBeTruthy();
  });

  it("成员只看到账号栏，仍能修改自己的密码，不请求管理接口", async () => {
    const api = setup("listener");
    renderApp({ path: "/settings/account" });
    await accountHeading();
    const nav = within(screen.getByRole("navigation", { name: "设置" }));
    expect(nav.getAllByRole("link").map((link) => link.textContent)).toEqual(["账号"]);
    expect(within(screen.getByRole("main")).getByText("listener")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "修改密码" }));
    expect(await screen.findByRole("dialog", { name: "修改密码" })).toBeTruthy();
    for (const path of managementApis) expect(api.requestsTo("GET", path)).toHaveLength(0);
  });

  it.each(managementPaths)("成员直接访问 %s 被拦截，所有管理接口均未发请求", async (path) => {
    const api = setup("listener");
    renderApp({ path });
    expect(await screen.findByRole("heading", { level: 1, name: "需要管理员权限" })).toBeTruthy();
    expect(within(screen.getByRole("navigation", { name: "设置" })).getAllByRole("link").map((link) => link.textContent)).toEqual(["账号"]);
    for (const apiPath of managementApis) expect(api.requestsTo("GET", apiPath)).toHaveLength(0);
  });

  it("/settings 默认进入账号栏并替换历史项", async () => {
    setup();
    const { history } = renderApp({ path: "/settings" });
    await accountHeading();
    expect(history.location.pathname).toBe("/settings/account");
    expect(history.length).toBe(1);
  });
});

describe("旧书签迁移", () => {
  it.each(legacyAddresses)("%s 保留查询参数顺序和 hash，替换旧历史项", async (oldAddress, newAddress) => {
    // 成员也要先迁移地址再做权限检查，避免页面自身的分页修正干扰路由合同。
    setup("listener");
    const { history } = renderApp({ path: "/settings/account" });
    await accountHeading();
    await act(async () => { history.push(oldAddress); });
    await screen.findByRole("heading", { level: 1, name: "需要管理员权限" });
    await waitFor(() => expectAddress(history.location.href, newAddress));
    expect(history.length).toBe(2);
    await act(async () => { history.back(); });
    await accountHeading();
    expect(history.location.href).toBe("/settings/account");
    await act(async () => { history.forward(); });
    await screen.findByRole("heading", { level: 1, name: "需要管理员权限" });
    expectAddress(history.location.href, newAddress);
    expect(history.length).toBe(2);
  });

  it.each(legacyAddresses)("匿名访问 %s：登录页记住新地址，登录后仍保留查询串和 hash", async (oldAddress, newAddress) => {
    const api = setup(null);
    const { history } = renderApp({ path: oldAddress });
    await screen.findByRole("heading", { level: 1, name: "登录" });
    expect(history.location.pathname).toBe("/login");
    const redirect = new URLSearchParams(history.location.search).get("redirect");
    expect(redirect).not.toBeNull();
    expectAddress(redirect ?? "", newAddress);
    for (const path of managementApis) expect(api.requestsTo("GET", path)).toHaveLength(0);
    await userEvent.type(screen.getByLabelText("用户名"), "listener");
    await userEvent.type(screen.getByLabelText("密码"), "listener-password");
    await userEvent.click(screen.getByRole("button", { name: "登录" }));
    await screen.findByRole("heading", { level: 1, name: "需要管理员权限" });
    expectAddress(history.location.href, newAddress);
    for (const path of managementApis) expect(api.requestsTo("GET", path)).toHaveLength(0);
  });
});
