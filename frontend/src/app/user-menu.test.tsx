import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { deferred } from "../test/deferred";
import { installFakeApi, installFakeSession, installOverviewApi } from "../test/fake-api";
import { sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

async function setup(path = "/") {
  const api = installFakeApi();
  const session = installFakeSession(api, { current: "curator" });
  installOverviewApi(api);
  const rendered = renderApp({ path });
  await screen.findByRole("heading", { level: 1, name: path.startsWith("/settings") ? "账号" : "概览" });
  const trigger = screen.getByRole("button", { name: "curator，管理员" });
  return { api, session, trigger, ...rendered };
}

describe("顶栏头像菜单", () => {
  it("头像读屏名包含用户名与角色，向下打开以用户名分组的“账号”菜单", async () => {
    const { trigger } = await setup();
    expect(within(trigger).getByText("C").getAttribute("aria-hidden")).toBe("true");
    expect(trigger.textContent).toBe("C");
    expect(screen.queryByText("curator", { exact: true })).toBeNull();
    expect(screen.queryByRole("link", { name: "设置" })).toBeNull();
    const bounds = new DOMRect(200, 12, 32, 32);
    vi.spyOn(trigger, "getBoundingClientRect").mockReturnValue(bounds);
    await userEvent.click(trigger);
    const menu = screen.getByRole("menu", { name: "账号" });
    const group = within(menu).getByRole("group", { name: "curator" });
    expect(within(group).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["账号", "设置"]);
    expect(within(menu).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["账号", "设置", "退出登录"]);
    expect(menu.style.top).toBe(`${bounds.bottom + 4}px`);
    expect(trigger.getAttribute("aria-expanded")).toBe("true");
    expect(trigger.getAttribute("aria-controls")).toBe(menu.id);
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
  });

  it.each(["{Enter}", " ", "{ArrowDown}"])("%s 打开后可上下循环、Home/End 并用 Esc 归还焦点", async (key) => {
    const user = userEvent.setup();
    const { trigger } = await setup();
    trigger.focus();
    await user.keyboard(key);
    const account = screen.getByRole("menuitem", { name: "账号" });
    const settings = screen.getByRole("menuitem", { name: "设置" });
    const logout = screen.getByRole("menuitem", { name: "退出登录" });
    expect(document.activeElement).toBe(account);
    await user.keyboard("{ArrowUp}");
    expect(document.activeElement).toBe(logout);
    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(account);
    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(settings);
    await user.keyboard("{End}");
    expect(document.activeElement).toBe(logout);
    await user.keyboard("{Home}");
    expect(document.activeElement).toBe(account);
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("上箭头打开聚焦退出登录，Tab 关闭而不困住焦点", async () => {
    const user = userEvent.setup();
    const { trigger } = await setup();
    trigger.focus();
    await user.keyboard("{ArrowUp}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "退出登录" }));
    await user.tab();
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).not.toBe(trigger);
  });

  it("键盘在头像菜单里选择“设置”进入账号栏，焦点移到主内容", async () => {
    const user = userEvent.setup();
    const { trigger, history } = await setup();
    trigger.focus();
    await user.keyboard("{Enter}{ArrowDown}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "设置" }));
    await user.keyboard("{Enter}");
    await screen.findByRole("heading", { level: 1, name: "账号" });
    expect(history.location.pathname).toBe("/settings/account");
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(screen.getByRole("main"));
  });

  it("直接进入账号栏时仍能打开头像菜单，设置分栏里的账号链接标记为当前", async () => {
    const { trigger } = await setup("/settings/account");
    const settingsNav = screen.getByRole("navigation", { name: "设置" });
    expect(within(settingsNav).getByRole("link", { name: "账号" }).getAttribute("aria-current")).toBe("page");
    await userEvent.click(trigger);
    expect(screen.getByRole("menu", { name: "账号" })).toBeTruthy();
  });

  it("在“账号”菜单里选“账号”进入账号栏", async () => {
    const { trigger, history } = await setup();
    await userEvent.click(trigger);
    await userEvent.click(screen.getByRole("menuitem", { name: "账号" }));
    await screen.findByRole("heading", { level: 1, name: "账号" });
    expect(history.location.pathname).toBe("/settings/account");
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("退出进行中显示忙碌且不提前离开；失败保留会话，重试成功才去登录页", async () => {
    const { api, session, trigger, history, runtime } = await setup();
    const generation = runtime.session.currentGeneration();
    const gate = deferred<Response>();
    api.on("POST", "/api/v1/auth/logout", () => gate.promise);
    await userEvent.click(trigger);
    await userEvent.click(screen.getByRole("menuitem", { name: "退出登录" }));
    const busy = await screen.findByRole("button", { name: "curator，正在退出…" });
    expect(busy).toBe(trigger);
    expect(busy.getAttribute("aria-busy")).toBe("true");
    expect(screen.queryByRole("menu")).toBeNull();
    // 退出进行中再打开菜单，“退出登录”不可选。
    await userEvent.click(trigger);
    expect(screen.getByRole<HTMLButtonElement>("menuitem", { name: "退出登录" }).disabled).toBe(true);
    await userEvent.keyboard("{Escape}");
    expect(history.location.pathname).toBe("/");
    expect(session.current).toBe("curator");
    gate.resolve(sampleResponse("errors/database-unavailable"));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("退出失败");
    expect(within(alert).getByRole("button", { name: "重试" })).toBeTruthy();
    expect(screen.getByRole("heading", { level: 1, name: "概览" })).toBeTruthy();
    expect(history.location.pathname).toBe("/");
    expect(session.current).toBe("curator");
    expect(runtime.session.currentGeneration()).toBe(generation);
    expect(api.requestsTo("POST", "/api/v1/auth/logout")).toHaveLength(1);
    expect(screen.getByRole("button", { name: "curator，管理员" })).toBe(trigger);
    expect(trigger.hasAttribute("aria-busy")).toBe(false);
    api.on("POST", "/api/v1/auth/logout", () => {
      session.current = null;
      return sampleResponse("auth/logout");
    });
    await userEvent.click(within(alert).getByRole("button", { name: "重试" }));
    await screen.findByRole("heading", { level: 1, name: "登录" });
    expect(history.location.pathname).toBe("/login");
    expect(history.location.search).toBe("");
    expect(api.requestsTo("POST", "/api/v1/auth/logout")).toHaveLength(2);
    expect(runtime.queryClient.getQueryCache().getAll()).toHaveLength(0);
  });
});
