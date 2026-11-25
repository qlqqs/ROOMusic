import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { decode as d } from "../shared/api";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const connectionPath = "/api/v1/me/subsonic";
const confirmPath = "/api/v1/me/subsonic/confirm-password";
const adminPath = "/api/v1/admin/subsonic";
const passwordBody = d.defineDecoder(d.object({ password: d.string() }));
const enabledBody = d.defineDecoder(d.object({ enabled: d.boolean() }));

function setup({ current = "listener", enabled = true, ready = false }: { current?: "curator" | "listener"; enabled?: boolean; ready?: boolean } = {}) {
  const api = installFakeApi();
  const session = installFakeSession(api, { current });
  const state = { enabled, ready };
  api.on("GET", connectionPath, () => jsonResponse(200, { enabled: state.enabled, ready: state.enabled && state.ready, username: current, jukebox_zone_id: null }));
  api.on("POST", confirmPath, ({ body }) => {
    if (passwordBody(body).password !== session.accounts[current]?.password) {
      return jsonResponse(403, { error: { code: "current_password_incorrect", message: "密码不对" }, request_id: "r" });
    }
    state.ready = true;
    return new Response(null, { status: 204 });
  });
  api.on("GET", adminPath, () => jsonResponse(200, { enabled: state.enabled }));
  api.on("PUT", adminPath, ({ body }) => {
    state.enabled = enabledBody(body).enabled;
    if (!state.enabled) state.ready = false;
    return jsonResponse(200, { enabled: state.enabled });
  });
  return { api, state };
}

const clientsGroup = async () => within((await screen.findByRole("heading", { level: 2, name: "第三方客户端" })).closest("section") as HTMLElement);

describe("第三方客户端：账号页", () => {
  it("实例关闭时不显示这一组", async () => {
    const { api } = setup({ enabled: false });
    renderApp({ path: "/settings/account" });
    await screen.findByRole("heading", { level: 1, name: "账号" });
    await waitFor(() => expect(api.requestsTo("GET", connectionPath)).toHaveLength(1));
    expect(screen.queryByRole("heading", { level: 2, name: "第三方客户端" })).toBeNull();
  });

  it("显示服务器地址与用户名；已就绪时没有确认入口", async () => {
    setup({ ready: true });
    renderApp({ path: "/settings/account" });
    const group = await clientsGroup();
    expect(group.getByText(`${window.location.origin}/`)).toBeTruthy();
    expect(group.getByText("listener")).toBeTruthy();
    expect(group.getByText("已就绪")).toBeTruthy();
    expect(group.queryByRole("button", { name: "确认密码" })).toBeNull();
  });

  it("只用键盘确认密码：密码不对留在对话框，确认后变成已就绪，焦点回到这一组的标题", async () => {
    const user = userEvent.setup();
    const { api } = setup();
    renderApp({ path: "/settings/account" });
    const group = await clientsGroup();
    const trigger = group.getByRole("button", { name: "确认密码" });
    trigger.focus();
    await user.keyboard("{Enter}");
    const dialog = within(await screen.findByRole("dialog", { name: "确认登录密码" }));
    const field = dialog.getByLabelText("登录密码");
    await user.click(field);
    await user.keyboard("wrong-password{Enter}");
    expect(await dialog.findByText("密码不对。")).toBeTruthy();
    expect(document.activeElement).toBe(field);
    await user.clear(field);
    await user.keyboard("listener-password{Enter}");
    const done = await dialog.findByRole("button", { name: "完成" });
    expect(document.activeElement).toBe(done);
    expect(dialog.getByText("已确认，客户端现在可以登录了。")).toBeTruthy();
    await user.keyboard("{Enter}");
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(await group.findByText("已就绪")).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("heading", { level: 2, name: "第三方客户端" }));
    expect(api.requestsTo("POST", confirmPath)).toHaveLength(2);
  });

  it("空密码不发请求，提示填写", async () => {
    const user = userEvent.setup();
    const { api } = setup();
    renderApp({ path: "/settings/account" });
    await user.click((await clientsGroup()).getByRole("button", { name: "确认密码" }));
    const dialog = within(await screen.findByRole("dialog", { name: "确认登录密码" }));
    await user.click(dialog.getByRole("button", { name: "确认" }));
    expect(await dialog.findByText("请输入登录密码。")).toBeTruthy();
    expect(api.requestsTo("POST", confirmPath)).toHaveLength(0);
  });
});

describe("第三方客户端：管理开关", () => {
  it("关闭前确认一次，确认后发送关闭；再打开直接生效", async () => {
    const user = userEvent.setup();
    const { api } = setup({ current: "curator" });
    renderApp({ path: "/settings/clients" });
    expect(await screen.findByRole("heading", { level: 1, name: "第三方客户端" })).toBeTruthy();
    const toggle = await screen.findByRole("switch", { name: "允许第三方客户端连接" });
    expect((toggle as HTMLInputElement).checked).toBe(true);
    expect(toggle.getAttribute("aria-describedby")).not.toBeNull();
    toggle.focus();
    await user.keyboard(" ");
    const dialog = within(await screen.findByRole("dialog", { name: "关闭第三方客户端连接？" }));
    expect(dialog.getByText(/保存的登录密码副本会全部删除/)).toBeTruthy();
    expect(api.requestsTo("PUT", adminPath)).toHaveLength(0);
    await user.click(dialog.getByRole("button", { name: "关闭" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect((screen.getByRole("switch", { name: "允许第三方客户端连接" }) as HTMLInputElement).checked).toBe(false));
    await user.click(screen.getByRole("switch", { name: "允许第三方客户端连接" }));
    await waitFor(() => expect((screen.getByRole("switch", { name: "允许第三方客户端连接" }) as HTMLInputElement).checked).toBe(true));
    expect(api.requestsTo("PUT", adminPath).map((request) => enabledBody(request.body).enabled)).toEqual([false, true]);
  });

  it("取消确认时开关保持开启，不发请求", async () => {
    const user = userEvent.setup();
    const { api } = setup({ current: "curator" });
    renderApp({ path: "/settings/clients" });
    await user.click(await screen.findByRole("switch", { name: "允许第三方客户端连接" }));
    await user.click(within(await screen.findByRole("dialog", { name: "关闭第三方客户端连接？" })).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect((screen.getByRole("switch", { name: "允许第三方客户端连接" }) as HTMLInputElement).checked).toBe(true);
    expect(api.requestsTo("PUT", adminPath)).toHaveLength(0);
  });
});
