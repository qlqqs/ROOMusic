import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const proxyPath = "/api/v1/admin/network/proxy";
const testPath = "/api/v1/admin/network/proxy/test";

type Proxy = { enabled: boolean; scheme: "http" | "socks5"; host: string; port: number | null; username: string; password_set: boolean; version: number };
const empty: Proxy = { enabled: false, scheme: "http", host: "", port: null, username: "", password_set: false, version: 0 };

function setup(initial: Partial<Proxy> = {}, testResult: Record<string, unknown> = { ok: true, code: null, latency_ms: 230 }) {
  const api = installFakeApi();
  installFakeSession(api, { current: "curator" });
  let state: Proxy = { ...empty, ...initial };
  api.on("GET", proxyPath, () => jsonResponse(200, state));
  api.on("PUT", proxyPath, ({ body }) => {
    const draft = body as Partial<Proxy> & { password?: string; version: number };
    if (draft.version !== state.version) return jsonResponse(409, { error: { code: "version_conflict", message: "版本冲突" }, request_id: "r" });
    const { password, version: _version, ...rest } = draft;
    state = { ...state, ...rest, password_set: password === undefined ? state.password_set : password !== "", version: state.version + 1 };
    return jsonResponse(200, state);
  });
  api.on("POST", testPath, () => jsonResponse(200, testResult));
  return api;
}

async function openPage() {
  renderApp({ path: "/settings/network" });
  return screen.findByRole<HTMLInputElement>("switch", { name: "通过代理访问外网" });
}

describe("设置 › 网络", () => {
  it("开启代理、填写地址与认证后保存，密码只随这一次请求发送", async () => {
    const user = userEvent.setup();
    const api = setup();
    const toggle = await openPage();
    expect(toggle.checked).toBe(false);
    expect(screen.queryByRole("textbox", { name: "代理主机" })).toBeNull();

    await user.click(toggle);
    await user.click(screen.getByRole("radio", { name: "SOCKS5" }));
    await user.type(screen.getByRole("textbox", { name: "代理主机" }), "192.168.1.2");
    const save = screen.getByRole<HTMLButtonElement>("button", { name: "保存" });
    expect(save.disabled).toBe(true);
    await user.type(screen.getByRole("textbox", { name: "代理端口" }), "7891");
    await user.type(screen.getByRole("textbox", { name: "用户名" }), "music");
    await user.type(screen.getByLabelText("密码"), "secret-pass");
    await user.click(save);

    await waitFor(() => expect(api.requestsTo("PUT", proxyPath)).toHaveLength(1));
    expect(api.requestsTo("PUT", proxyPath)[0]?.body).toEqual({ enabled: true, scheme: "socks5", host: "192.168.1.2", port: 7891, username: "music", password: "secret-pass", version: 0 });
    expect(await screen.findByText("已保存")).toBeTruthy();
    expect(screen.getByText("已设置")).toBeTruthy();
    expect(screen.getByRole("button", { name: "更换代理密码" })).toBeTruthy();
  });

  it("端口不是 1–65535 的整数时给出提示且不能保存", async () => {
    const user = userEvent.setup();
    setup({ enabled: true, host: "proxy.lan", port: 8080 });
    await openPage();
    const port = screen.getByRole("textbox", { name: "代理端口" });
    await user.clear(port);
    await user.type(port, "70000");
    expect(screen.getByText("端口是 1 到 65535 的整数。")).toBeTruthy();
    expect(port.getAttribute("aria-invalid")).toBe("true");
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "保存" }).disabled).toBe(true);
  });

  it("已保存的密码不回显；不改密码保存时不带 password，清除时发送空串", async () => {
    const user = userEvent.setup();
    const api = setup({ enabled: true, scheme: "http", host: "proxy.lan", port: 8080, username: "u", password_set: true, version: 3 });
    await openPage();
    expect(screen.queryByLabelText("密码", { selector: "input" })).toBeNull();

    const host = screen.getByRole("textbox", { name: "代理主机" });
    await user.clear(host);
    await user.type(host, "proxy2.lan");
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.requestsTo("PUT", proxyPath)).toHaveLength(1));
    expect(api.requestsTo("PUT", proxyPath)[0]?.body).toEqual({ enabled: true, scheme: "http", host: "proxy2.lan", port: 8080, username: "u", version: 3 });

    await user.click(await screen.findByRole("button", { name: "清除代理密码" }));
    expect(screen.getByText("保存后清除")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.requestsTo("PUT", proxyPath)).toHaveLength(2));
    expect(api.requestsTo("PUT", proxyPath)[1]?.body).toMatchObject({ password: "", version: 4 });
    expect(await screen.findByLabelText("密码")).toBeTruthy();
  });

  it("测试连接按当前草稿发起，不必先保存；成功显示用时", async () => {
    const user = userEvent.setup();
    const api = setup({ enabled: true, host: "proxy.lan", port: 8080, password_set: true });
    await openPage();
    const port = screen.getByRole("textbox", { name: "代理端口" });
    await user.clear(port);
    await user.type(port, "3128");
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    expect(await screen.findByText("代理可以用，用时 230 毫秒")).toBeTruthy();
    expect(api.requestsTo("POST", testPath).map(({ body }) => body)).toEqual([{ scheme: "http", host: "proxy.lan", port: 3128, username: "" }]);
    expect(api.requestsTo("PUT", proxyPath)).toHaveLength(0);
  });

  it("测试失败按原因给出一句话", async () => {
    const user = userEvent.setup();
    setup({ enabled: true, host: "proxy.lan", port: 8080 }, { ok: false, code: "proxy_auth_failed", latency_ms: null });
    await openPage();
    await user.click(screen.getByRole("button", { name: "测试连接" }));
    expect(await screen.findByText("代理拒绝了用户名或密码。")).toBeTruthy();
  });

  it("别处改过（409）时提示刷新", async () => {
    const user = userEvent.setup();
    const api = setup({ enabled: false, version: 2 });
    api.on("PUT", proxyPath, () => jsonResponse(409, { error: { code: "version_conflict", message: "版本冲突" }, request_id: "r" }));
    const toggle = await openPage();
    await user.click(toggle);
    await user.type(screen.getByRole("textbox", { name: "代理主机" }), "proxy.lan");
    await user.type(screen.getByRole("textbox", { name: "代理端口" }), "8080");
    await user.click(screen.getByRole("button", { name: "保存" }));
    expect(await screen.findByText("设置刚在别处改过，刷新页面后再改。")).toBeTruthy();
  });

  it("关闭代理保存时保留已填的地址", async () => {
    const user = userEvent.setup();
    const api = setup({ enabled: true, scheme: "socks5", host: "proxy.lan", port: 1080, version: 1 });
    const toggle = await openPage();
    await user.click(toggle);
    expect(screen.queryByRole("textbox", { name: "代理主机" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.requestsTo("PUT", proxyPath)).toHaveLength(1));
    expect(api.requestsTo("PUT", proxyPath)[0]?.body).toEqual({ enabled: false, scheme: "socks5", host: "proxy.lan", port: 1080, username: "", version: 1 });
  });
});
