import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { decodeLanDevice, decodeLanDevices, type LanDeviceDTO } from "../features/lan-devices/api/decoders";
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
const settingsPatch = d.defineDecoder(d.object({ enabled: d.optional(d.boolean()), shared: d.optional(d.boolean()), upnp_enabled: d.optional(d.boolean()), cast_enabled: d.optional(d.boolean()), airplay_enabled: d.optional(d.boolean()), slimproto_enabled: d.optional(d.boolean()) }));
const addressBody = d.defineDecoder(d.object({ address: d.string() }));
const description = (element: HTMLElement) => document.getElementById(element.getAttribute("aria-describedby") ?? "")?.textContent;

function setup({ enabled = false, items = [], current = "curator", slimprotoUnavailable = null }: { enabled?: boolean; items?: LanDeviceDTO[]; current?: string; slimprotoUnavailable?: string | null } = {}) {
  const api = installFakeApi();
  const session = installFakeSession(api, { current });
  installMembersApi(api, session);
  let settings = { enabled, shared: true, upnp_enabled: true, cast_enabled: true, airplay_enabled: true, slimproto_enabled: true, slimproto_unavailable: slimprotoUnavailable };
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

  it("总开关打开后分别开关两种协议", async () => {
    const user = userEvent.setup();
    const { api } = setup({ enabled: true });
    renderSettings();
    const cast = await screen.findByRole<HTMLInputElement>("switch", { name: "Google Cast" });
    expect(cast.checked).toBe(true);
    expect(screen.getByRole<HTMLInputElement>("switch", { name: "UPnP/DLNA" }).checked).toBe(true);
    await user.click(cast);
    await waitFor(() => expect(cast.checked).toBe(false));
    expect(api.requestsTo("PUT", settingsPath).map(({ body }) => body)).toEqual([{ cast_enabled: false }]);
  });

  it("总开关关着时不显示协议开关", async () => {
    setup();
    renderSettings();
    await useLan();
    expect(screen.queryByRole("switch", { name: "Google Cast" })).toBeNull();
  });

  it("手动添加 Google Cast：选类型后提交 kind 与地址，行上标出类型与编码", async () => {
    const user = userEvent.setup();
    const { api, devices } = setup({ enabled: true });
    const cast = decodeLanDevice(sampleBody("upnp/cast-manual-added"));
    api.on("POST", devicesPath, () => {
      devices.push(cast);
      return sampleResponse("upnp/cast-manual-added");
    });
    renderSettings();
    await user.click(await screen.findByRole("button", { name: "手动添加" }));
    const dialog = within(screen.getByRole("dialog", { name: "手动添加设备" }));
    await user.click(dialog.getByRole("radio", { name: "Google Cast" }));
    await user.type(dialog.getByRole("textbox", { name: "设备地址" }), "192.168.44.2:8009");
    await user.click(dialog.getByRole("button", { name: "添加" }));
    expect(await screen.findByText(cast.name)).toBeTruthy();
    expect(api.requestsTo("POST", devicesPath).map(({ body }) => body)).toEqual([{ kind: "cast", address: "192.168.44.2:8009" }]);
    const region = within(screen.getByRole("region", { name: "设备" }));
    expect(region.getByText("Google Cast")).toBeTruthy();
    expect(region.getByText(/MP3，最高 48 kHz，曲目之间可能有短暂停顿/)).toBeTruthy();
  });

  it("Google Cast 编码格式：FLAC 才有 96 kHz，保存写入选中值", async () => {
    const user = userEvent.setup();
    const cast = decodeLanDevice(sampleBody("upnp/cast-manual-added"));
    const { api } = setup({ enabled: true, items: [cast] });
    const formatPath = `${devicesPath}/${encodeURIComponent(cast.udn)}/format`;
    api.on("PUT", formatPath, ({ body }) => jsonResponse(200, body));
    renderSettings();
    await user.click(await screen.findByRole("button", { name: `${cast.name} 的更多操作` }));
    await user.click(screen.getByRole("menuitem", { name: `${cast.name} 的编码格式…` }));
    const dialog = within(screen.getByRole("dialog", { name: `“${cast.name}”的编码格式` }));
    expect(dialog.queryByRole("radio", { name: "96 kHz" })).toBeNull();
    await user.click(dialog.getByRole("radio", { name: "FLAC" }));
    await user.click(dialog.getByRole("radio", { name: "96 kHz" }));
    await user.click(dialog.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: `“${cast.name}”的编码格式` })).toBeNull());
    expect(api.requestsTo("PUT", formatPath).map(({ body }) => body)).toEqual([{ codec: "flac", max_sample_rate: 96000 }]);
  });

  it("AirPlay：音质对话框写入 48 kHz / 24 位；需要 PIN 的设备经配对对话框完成配对", async () => {
    const user = userEvent.setup();
    const base = decodeLanDevice(sampleBody("upnp/airplay-manual-added"));
    const appleTv = { ...base, name: "客厅 Apple TV", capabilities: { ...base.capabilities, airplay: base.capabilities.airplay === null ? null : { ...base.capabilities.airplay, requires_pin: true, paired: false } } };
    const { api } = setup({ enabled: true, items: [appleTv] });
    const path = `${devicesPath}/${encodeURIComponent(appleTv.udn)}`;
    api.on("PUT", `${path}/format`, ({ body }) => jsonResponse(200, body));
    api.on("POST", `${path}/pair/start`, () => new Response(null, { status: 204 }));
    api.on("POST", `${path}/pair/finish`, () => new Response(null, { status: 204 }));
    renderSettings();
    const region = within(await screen.findByRole("region", { name: "设备" }));
    expect(await region.findByText("待配对")).toBeTruthy();
    expect(region.getByText("AirPlay")).toBeTruthy();
    expect(region.getByText(/ALAC，最高 44.1 kHz \/ 16 位 · 精确对时/)).toBeTruthy();

    await user.click(screen.getByRole("button", { name: `${appleTv.name} 的更多操作` }));
    await user.click(screen.getByRole("menuitem", { name: `${appleTv.name} 的音质…` }));
    const quality = within(screen.getByRole("dialog", { name: `“${appleTv.name}”的音质` }));
    await user.click(quality.getByRole("radio", { name: /48 kHz \/ 24 位/ }));
    await user.click(quality.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(api.requestsTo("PUT", `${path}/format`).map(({ body }) => body)).toEqual([{ codec: "alac", max_sample_rate: 48000, max_bit_depth: 24 }]));

    await user.click(screen.getByRole("button", { name: `${appleTv.name} 的更多操作` }));
    await user.click(screen.getByRole("menuitem", { name: `与 ${appleTv.name} 配对…` }));
    const pair = within(screen.getByRole("dialog", { name: `与“${appleTv.name}”配对` }));
    await waitFor(() => expect(api.requestsTo("POST", `${path}/pair/start`)).toHaveLength(1));
    await user.type(pair.getByRole("textbox", { name: "屏幕上的 PIN" }), "1234");
    await user.click(pair.getByRole("button", { name: "配对" }));
    await waitFor(() => expect(api.requestsTo("POST", `${path}/pair/finish`).map(({ body }) => body)).toEqual([{ pin: "1234" }]));
  });

  it("SlimProto 开关：端口 3483 被占用时换成占用说明，关掉后恢复常规说明并写入 slimproto_enabled", async () => {
    const user = userEvent.setup();
    const { api } = setup({ enabled: true, slimprotoUnavailable: "port_in_use" });
    renderSettings();
    const slim = await screen.findByRole<HTMLInputElement>("switch", { name: "SlimProto" });
    expect(slim.checked).toBe(true);
    expect(description(slim)).toMatch(/端口 3483 被本机其他程序占用/);
    await user.click(slim);
    await waitFor(() => expect(slim.checked).toBe(false));
    expect(description(slim)).toMatch(/Squeezelite/);
    expect(api.requestsTo("PUT", settingsPath).map(({ body }) => body)).toEqual([{ slimproto_enabled: false }]);
  });

  it("SlimProto 开关：端口可用时显示常规说明", async () => {
    setup({ enabled: true });
    renderSettings();
    const slim = await screen.findByRole<HTMLInputElement>("switch", { name: "SlimProto" });
    expect(description(slim)).toMatch(/Squeezelite/);
    expect(description(slim)).not.toMatch(/3483/);
  });

  it("SlimProto 播放器行显示型号、固件与最高采样率；不支持 FLAC 的播放器标出“不支持”", async () => {
    const [slim] = decodeLanDevices(sampleBody("upnp/slimproto-list")).items;
    if (slim === undefined || slim.capabilities.slimproto == null) throw new Error("缺少 SlimProto 样本");
    const unsupported = { ...slim, udn: "slim:02:00:00:00:00:02", name: "车库", capabilities: { ...slim.capabilities, slimproto: { ...slim.capabilities.slimproto, unsupported: true } } };
    setup({ enabled: true, items: [slim, unsupported] });
    renderSettings();
    const region = within(await screen.findByRole("region", { name: "设备" }));
    expect(await region.findByText("书房")).toBeTruthy();
    expect(region.getAllByText("SlimProto")).toHaveLength(2);
    expect(region.getByText("squeezelite · 在线 · SqueezeLite 2.0.0 · FLAC，最高 192 kHz / 24 位 · 192.168.44.2")).toBeTruthy();
    expect(region.getByText(/播放器不支持 FLAC，暂时不能使用/)).toBeTruthy();
    expect(region.getAllByText("不支持")).toHaveLength(1);
  });

  it("SlimProto 音质：只列出不超过播放器声明的采样率，默认跟随播放器，保存写入采样率上限与音量方式", async () => {
    const user = userEvent.setup();
    const [slim] = decodeLanDevices(sampleBody("upnp/slimproto-list")).items;
    if (slim === undefined) throw new Error("缺少 SlimProto 样本");
    const { api } = setup({ enabled: true, items: [slim] });
    const formatPath = `${devicesPath}/${encodeURIComponent(slim.udn)}/format`;
    api.on("PUT", formatPath, ({ body }) => jsonResponse(200, body));
    renderSettings();
    const openDialog = async () => {
      await user.click(await screen.findByRole("button", { name: `${slim.name} 的更多操作` }));
      await user.click(screen.getByRole("menuitem", { name: `${slim.name} 的音质…` }));
      return within(screen.getByRole("dialog", { name: `“${slim.name}”的音质` }));
    };
    const dialogClosed = () => waitFor(() => expect(screen.queryByRole("dialog", { name: `“${slim.name}”的音质` })).toBeNull());

    let dialog = await openDialog();
    const rateGroup = within(dialog.getByRole("group", { name: "采样率上限（FLAC 无损）" }));
    expect(rateGroup.getAllByRole("radio").map((radio) => radio.parentElement?.textContent)).toEqual([
      "跟随播放器（192 kHz）",
      "44.1 kHz",
      "48 kHz",
      "88.2 kHz",
      "96 kHz",
      "176.4 kHz",
      "192 kHz",
    ]);
    expect(rateGroup.getByRole<HTMLInputElement>("radio", { name: "跟随播放器（192 kHz）" }).checked).toBe(true);
    expect(dialog.getByRole<HTMLInputElement>("radio", { name: "在 ROOMusic 里调" }).checked).toBe(true);
    await user.click(dialog.getByRole("button", { name: "保存" }));
    await dialogClosed();

    dialog = await openDialog();
    await user.click(dialog.getByRole("radio", { name: "96 kHz" }));
    await user.click(dialog.getByRole("radio", { name: "固定，在功放或解码器上调" }));
    await user.click(dialog.getByRole("button", { name: "保存" }));
    await dialogClosed();
    expect(api.requestsTo("PUT", formatPath).map(({ body }) => body)).toEqual([
      { codec: "flac", max_sample_rate: null, volume: "number" },
      { codec: "flac", max_sample_rate: 96000, volume: "fixed" },
    ]);
  });

  it("手动添加不提供 SlimProto：播放器自己连上来", async () => {
    const user = userEvent.setup();
    setup({ enabled: true });
    renderSettings();
    await user.click(await screen.findByRole("button", { name: "手动添加" }));
    const kinds = within(within(screen.getByRole("dialog", { name: "手动添加设备" })).getByRole("group", { name: "设备类型" }));
    expect(kinds.getAllByRole("radio").map((radio) => radio.parentElement?.textContent)).toEqual(["UPnP/DLNA", "Google Cast", "AirPlay"]);
    expect(kinds.queryByRole("radio", { name: "SlimProto" })).toBeNull();
  });
});
