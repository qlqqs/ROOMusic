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
  const trigger = within(region).getByRole("button", { name: "curator 管理员" });
  return { api, session, region, trigger, ...rendered };
}

describe("侧栏用户菜单 A7", () => {
  it("头像首字、用户名和角色归为一个按钮，点击向上弹出设置与退出登录", async () => {
    const { region, trigger } = await setup();
    expect(within(trigger).getByText("C").getAttribute("aria-hidden")).toBe("true");
    expect(within(trigger).getByText("curator")).toBeTruthy();
    expect(within(trigger).getByText("管理员")).toBeTruthy();
    expect(within(region).getAllByRole("button")).toEqual([trigger]);
    expect(within(region).queryByRole("button", { name: "修改密码" })).toBeNull();
    expect(trigger.hasAttribute("data-current")).toBe(false);
    vi.spyOn(trigger, "getBoundingClientRect").mockReturnValueOnce(new DOMRect(16, 600, 220, 44));
    await userEvent.click(trigger);
    const menu = screen.getByRole("menu", { name: "账号" });
    expect(within(menu).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["设置", "退出登录"]);
    expect(menu.style.bottom).toBe(`${window.innerHeight - 600 + 4}px`);
    expect(menu.style.width).toBe("220px");
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
    const settings = screen.getByRole("menuitem", { name: "设置" });
    const logout = screen.getByRole("menuitem", { name: "退出登录" });
    expect(document.activeElement).toBe(settings);
    await user.keyboard("{ArrowUp}");
    expect(document.activeElement).toBe(logout);
    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(settings);
    await user.keyboard("{End}");
    expect(document.activeElement).toBe(logout);
    await user.keyboard("{Home}");
    expect(document.activeElement).toBe(settings);
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

  it("键盘选择设置进入账号栏，用户按钮带当前样式但不是当前页链接", async () => {
    const user = userEvent.setup();
    const { trigger, history } = await setup();
    trigger.focus();
    await user.keyboard("{ArrowDown}{Enter}");
    await screen.findByRole("heading", { level: 1, name: "账号" });
    expect(history.location.pathname).toBe("/settings/account");
    expect(screen.queryByRole("menu")).toBeNull();
    expect(trigger.hasAttribute("data-current")).toBe(true);
    expect(trigger.hasAttribute("aria-current")).toBe(false);
    expect(document.activeElement).toBe(screen.getByRole("main"));
  });

  it("直接进入设置页时用户按钮也标记为当前", async () => {
    const { trigger } = await setup("/settings/account");
    expect(trigger.hasAttribute("data-current")).toBe(true);
  });

  it("退出进行中显示忙碌且不提前离开；失败保留会话，重试成功才去登录页", async () => {
    const { api, session, trigger, history, runtime, region } = await setup();
    const generation = runtime.session.currentGeneration();
    const gate = deferred<Response>();
    api.on("POST", "/api/v1/auth/logout", () => gate.promise);
    await userEvent.click(trigger);
    await userEvent.click(screen.getByRole("menuitem", { name: "退出登录" }));
    await waitFor(() => expect(trigger.getAttribute("aria-busy")).toBe("true"));
    expect(screen.queryByRole("menu")).toBeNull();
    expect(within(trigger).getByText("正在退出…")).toBeTruthy();
    expect(within(region).getByRole("button", { name: "curator 正在退出…" })).toBe(trigger);
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
    expect(trigger.getAttribute("aria-busy")).not.toBe("true");
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

describe("窄屏导航里的用户菜单 A8", () => {
  it.each(["/", "/settings/account"])("从 %s 选择设置后关闭导航对话框，进入账号栏", async (path) => {
    vi.stubGlobal("innerWidth", 375);
    const { history } = await setup(path);
    await userEvent.click(screen.getByRole("button", { name: "菜单" }));
    const dialog = screen.getByRole("dialog", { name: "菜单" });
    const region = within(dialog).getByRole("region", { name: "当前用户" });
    const trigger = within(region).getByRole("button", { name: "curator 管理员" });
    expect(within(trigger).getByText("C")).toBeTruthy();
    expect(within(region).queryByRole("button", { name: "修改密码" })).toBeNull();
    await userEvent.click(trigger);
    const menu = within(dialog).getByRole("menu", { name: "账号" });
    expect(menu.closest("dialog")).toBe(dialog);
    expect(within(menu).getByRole("menuitem", { name: "退出登录" })).toBeTruthy();
    await userEvent.click(within(menu).getByRole("menuitem", { name: "设置" }));
    await screen.findByRole("heading", { level: 1, name: "账号" });
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "菜单" })).toBeNull());
    expect(history.location.pathname).toBe("/settings/account");
    expect(screen.queryByRole("menu")).toBeNull();
    expect(screen.getByRole("link", { name: "账号" }).getAttribute("aria-current")).toBe("page");
  });
});
