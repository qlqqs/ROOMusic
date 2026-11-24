import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { decodeLanDevice, type LanDeviceDTO } from "../features/lan-devices/api/decoders";
import { decode as d } from "../shared/api";
import { sampleBody } from "../test/api-samples";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { installMembersApi } from "../test/fake-members";
import { jsonResponse, sampleResponse } from "../test/http";
import { renderApp } from "../test/render-app";

const settingsPath = "/api/v1/admin/upnp/settings";
const devicesPath = "/api/v1/admin/upnp/devices";
const added = decodeLanDevice(sampleBody("upnp/manual-added"));
const devicePath = `${devicesPath}/${encodeURIComponent(added.udn)}`;
const useLan = () => screen.findByRole<HTMLInputElement>("switch", { name: "使用局域网里的播放设备" });
const shared = () => screen.getByRole<HTMLInputElement>("switch", { name: "所有成员共用" });
const settingsPatch = d.defineDecoder(d.object({ enabled: d.optional(d.boolean()), shared: d.optional(d.boolean()) }));
const addressBody = d.defineDecoder(d.object({ address: d.string() }));

function setup({ enabled = false, items = [], current = "curator" }: { enabled?: boolean; items?: LanDeviceDTO[]; current?: string } = {}) {
  const api = installFakeApi();
  const session = installFakeSession(api, { current });
  installMembersApi(api, session);
  let settings = { enabled, shared: true };
  const devices = [...items];
  api.on("GET", settingsPath, () => jsonResponse(200, settings));
  api.on("PUT", settingsPath, ({ body }) => {
    settings = { ...settings, ...settingsPatch(body) };
    return jsonResponse(200, settings);
  });
  api.on("GET", devicesPath, () => jsonResponse(200, { items: devices }));
  return { api, devices, session };
}

function renderSettings() {
  return renderApp({ path: "/settings/devices" });
}

describe("设置 › 局域网设备", () => {
  it("读取默认开关并分别保存，空设备提示跟随启用状态", async () => {
    const user = userEvent.setup();
    const { api } = setup();
    renderSettings();
    const enabled = await useLan();
    expect(enabled.checked).toBe(false);
    expect(shared().checked).toBe(true);
    expect(await screen.findByText("打开“使用局域网里的播放设备”后开始查找。")).toBeTruthy();
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "手动添加" }).disabled).toBe(true);

    await user.click(enabled);
    expect(await screen.findByText(/还没有找到设备/)).toBeTruthy();
    expect(enabled.checked).toBe(true);
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "手动添加" }).disabled).toBe(false);
    await waitFor(() => expect(shared().disabled).toBe(false));
    await user.click(shared());
    await waitFor(() => expect(shared().disabled).toBe(false));
    expect(shared().checked).toBe(false);
    expect(api.requestsTo("PUT", settingsPath).map(({ body }) => body)).toEqual([{ enabled: true }, { shared: false }]);
  });

  it.each(["使用局域网里的播放设备", "所有成员共用"])("写入“%s”失败时提示并回到服务端值", async (name) => {
    const user = userEvent.setup();
    const { api } = setup();
    api.on("PUT", settingsPath, () => jsonResponse(503, { error: { code: "unavailable", message: "暂不可用" } }));
    renderSettings();
    const toggle = await screen.findByRole<HTMLInputElement>("switch", { name });
    const initial = toggle.checked;
    await user.click(toggle);
    expect(await screen.findByText("开关没有改成")).toBeTruthy();
    expect(toggle.checked).toBe(initial);
    expect(toggle.disabled).toBe(false);
  });

  it("列出格式、采样率与位深，并说明固定音量、离线和格式未知", async () => {
    setup({ enabled: true, items: [
      { ...added, name: "客厅功放", manual: false, capabilities: { ...added.capabilities, stream_formats: ["flac", "wav"], max_sample_rate: 96000 } },
      {
        ...added, udn: "uuid:offline-test", name: "书房音响", model: "", online: false,
        capabilities: { ...added.capabilities, volume: "fixed", format_declared: false },
      },
    ] });
    renderSettings();
    expect(await screen.findByText("客厅功放")).toBeTruthy();
    expect(screen.getByText(`Fake Renderer · 在线 · FLAC、WAV，最高 96 kHz / 24 位 · ${added.address}`)).toBeTruthy();
    expect(screen.getByText("书房音响")).toBeTruthy();
    expect(screen.getByText(`离线 · 没有声明支持的格式，先试 FLAC，最高 48 kHz / 24 位 · 音量在设备上调 · ${added.address}`)).toBeTruthy();
  });

  it("设备列表显示设备的描述地址", async () => {
    setup({ enabled: true, items: [added] });
    renderSettings();
    const devices = within(await screen.findByRole("region", { name: "设备" }));
    await devices.findByText(added.name);
    expect(devices.queryByText(added.address, { exact: false }), "设备行应显示接口返回的描述地址").not.toBeNull();
  });

  it("手动添加显示 invalid_address 的提示，修正后显示设备并可移除", async () => {
    const user = userEvent.setup();
    const { api, devices } = setup({ enabled: true });
    api.on("POST", devicesPath, ({ body }) => {
      if (addressBody(body).address === "192.168.44.2") {
        devices.push(added);
        return sampleResponse("upnp/manual-added");
      }
      return sampleResponse("upnp/invalid-address");
    });
    api.on("DELETE", devicePath, () => {
      devices.splice(0);
      return new Response(null, { status: 204 });
    });
    renderSettings();
    await user.click(await screen.findByRole("button", { name: "手动添加" }));
    const dialog = within(screen.getByRole("dialog", { name: "手动添加设备" }));
    const address = dialog.getByRole("textbox", { name: "设备地址" });
    await user.type(address, "invalid");
    await user.click(dialog.getByRole("button", { name: "添加" }));
    expect(await dialog.findByText(/需要设备的局域网 IP/)).toBeTruthy();

    await user.clear(address);
    await user.type(address, "192.168.44.2");
    await user.click(dialog.getByRole("button", { name: "添加" }));
    expect(await screen.findByText(added.name)).toBeTruthy();
    expect(screen.queryByRole("dialog", { name: "手动添加设备" })).toBeNull();
    await user.click(screen.getByRole("button", { name: `${added.name} 的更多操作` }));
    await user.click(screen.getByRole("menuitem", { name: `移除 ${added.name}` }));
    expect(await screen.findByText(/还没有找到设备/)).toBeTruthy();
    expect(api.requestsTo("DELETE", devicePath)).toHaveLength(1);
  });

  it("关闭共享才提供分配，勾选启用的普通成员后保存", async () => {
    const user = userEvent.setup();
    const { api, devices, session } = setup({ enabled: true, items: [added] });
    const grantsPath = `${devicePath}/grants`;
    const member = session.accounts["listener"];
    if (member === undefined) throw new Error("缺少测试成员");
    const user_ids = [member.id];
    api.on("PUT", grantsPath, ({ body }) => {
      expect(body).toEqual({ user_ids });
      devices[0] = { ...added, user_ids };
      return jsonResponse(200, { user_ids });
    });
    renderSettings();
    const menu = await screen.findByRole("button", { name: `${added.name} 的更多操作` });
    await user.click(menu);
    expect(screen.getByRole("menu")).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: /分配.*给成员/ })).toBeNull();
    await user.keyboard("{Escape}");
    await user.click(shared());

    expect(await screen.findByText(/只有管理员可用/)).toBeTruthy();
    await user.click(menu);
    await user.click(screen.getByRole("menuitem", { name: /分配.*给成员/ }));
    const dialog = within(screen.getByRole("dialog", { name: `哪些成员可以用“${added.name}”` }));
    const checkbox = await dialog.findByRole<HTMLInputElement>("checkbox", { name: "listener" });
    expect(dialog.getAllByRole("checkbox")).toEqual([checkbox]);
    expect(checkbox.checked).toBe(false);
    await user.click(checkbox);
    await user.click(dialog.getByRole("button", { name: "保存" }));

    expect(await screen.findByText(/分配给 1 位成员/)).toBeTruthy();
    expect(api.requestsTo("PUT", grantsPath).map(({ body }) => body)).toEqual([{ user_ids }]);
    await user.click(menu);
    await user.click(screen.getByRole("menuitem", { name: /分配.*给成员/ }));
    expect((await screen.findByRole<HTMLInputElement>("checkbox", { name: "listener" })).checked).toBe(true);
  });

  it("普通成员看不到栏目，直达也不会读局域网设备管理接口", async () => {
    const { api } = setup({ current: "listener" });
    renderSettings();
    expect(await screen.findByRole("heading", { name: "需要管理员权限" })).toBeTruthy();
    expect(within(screen.getByRole("navigation", { name: "设置" })).queryByRole("link", { name: "局域网设备" })).toBeNull();
    expect(api.requestsTo("GET", settingsPath)).toHaveLength(0);
    expect(api.requestsTo("GET", devicesPath)).toHaveLength(0);
  });
});
