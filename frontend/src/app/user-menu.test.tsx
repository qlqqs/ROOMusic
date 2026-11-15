import { screen, waitFor, within } from "@testing-library/react";
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
  const region = screen.getByRole("region", { name: "当前用户" });
  const settings = within(region).getByRole("link", { name: "设置" });
  const trigger = within(region).getByRole("button", { name: "账号" });
  return { api, session, region, settings, trigger, ...rendered };
}

describe("侧栏底部的当前用户 A7", () => {
  it("头像首字、用户名与角色是文字，右侧是齿轮链接与“账号”菜单，菜单向上弹出账号与退出登录", async () => {
    const { region, settings, trigger } = await setup();
    expect(within(region).getByText("C").getAttribute("aria-hidden")).toBe("true");
    expect(within(region).getByText("curator")).toBeTruthy();
    expect(within(region).getByText("管理员")).toBeTruthy();
    expect(within(region).getAllByRole("button")).toEqual([trigger]);
    expect(within(region).queryByRole("button", { name: "修改密码" })).toBeNull();
    expect(settings.getAttribute("href")).toBe("/settings");
    expect(settings.hasAttribute("aria-current")).toBe(false);
    vi.spyOn(trigger, "getBoundingClientRect").mockReturnValue(new DOMRect(200, 600, 32, 32));
    await userEvent.click(trigger);
    const menu = screen.getByRole("menu", { name: "账号" });
    expect(within(menu).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["账号", "退出登录"]);
    expect(menu.style.bottom).toBe(`${window.innerHeight - 600 + 4}px`);
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
    const logout = screen.getByRole("menuitem", { name: "退出登录" });
    expect(document.activeElement).toBe(account);
    await user.keyboard("{ArrowUp}");
    expect(document.activeElement).toBe(logout);
    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(account);
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

  it("键盘按齿轮一步进入账号栏，齿轮随之成为当前页链接", async () => {
    const user = userEvent.setup();
    const { settings, history } = await setup();
    settings.focus();
    await user.keyboard("{Enter}");
    await screen.findByRole("heading", { level: 1, name: "账号" });
    expect(history.location.pathname).toBe("/settings/account");
    expect(within(screen.getByRole("region", { name: "当前用户" })).getByRole("link", { name: "设置" }).getAttribute("aria-current")).toBe("page");
    expect(document.activeElement).toBe(screen.getByRole("main"));
  });

  it("直接进入设置页时齿轮也标记为当前", async () => {
    const { settings } = await setup("/settings/account");
    expect(settings.getAttribute("aria-current")).toBe("page");
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
    const { api, session, trigger, history, runtime, region } = await setup();
    const generation = runtime.session.currentGeneration();
    const gate = deferred<Response>();
    api.on("POST", "/api/v1/auth/logout", () => gate.promise);
    await userEvent.click(trigger);
    await userEvent.click(screen.getByRole("menuitem", { name: "退出登录" }));
    const row = within(region).getByText("curator").closest("[aria-busy]");
    await waitFor(() => expect(within(region).getByText("正在退出…")).toBeTruthy());
    expect(row?.getAttribute("aria-busy")).toBe("true");
    expect(screen.queryByRole("menu")).toBeNull();
    // 退出进行中再打开菜单，“退出登录”不可选。
    await userEvent.click(trigger);
    expect(screen.getByRole<HTMLButtonElement>("menuitem", { name: "退出登录" }).disabled).toBe(true);
    await userEvent.keyboard("{Escape}");
    expect(history.location.pathname).toBe("/");
    expect(session.current).toBe("curator");
    gate.resolve(sampleResponse("errors/database-unavailable"));
    const alert = await within(region).findByRole("alert");
    expect(alert.textContent).toContain("退出失败");
    expect(within(alert).getByRole("button", { name: "重试" })).toBeTruthy();
    expect(screen.getByRole("heading", { level: 1, name: "概览" })).toBeTruthy();
    expect(history.location.pathname).toBe("/");
    expect(session.current).toBe("curator");
    expect(runtime.session.currentGeneration()).toBe(generation);
    expect(api.requestsTo("POST", "/api/v1/auth/logout")).toHaveLength(1);
    expect(within(region).getByText("管理员")).toBeTruthy();
    expect(within(region).getByText("curator").closest("[aria-busy]")).toBeNull();
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
