import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ZoneApi } from "../features/player";
import type { ZoneCommand } from "../features/player/zones/api";
import type { Device, Zone } from "../features/player/zones/model";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { FakeAudio } from "../test/fake-audio";
import { fakeZoneServer, type FakeZoneServer } from "../test/fake-zones";
import { renderApp } from "../test/render-app";

// 第三方客户端（Subsonic）登记的设备：不出现在播放设备列表里；设置 › 播放可以改它的设置、删除它。

const CLIENT_DEVICE = "00000000-0000-4000-8000-00000000d0c1";
const CLIENT_ZONE = "00000000-0000-4000-8000-00000000e0c1";

type Harness = { server: FakeZoneServer; clientCommands: ZoneCommand[]; deleted: string[]; client: { device: Device; zone: Zone } | null };
const zones = vi.hoisted(() => ({ harness: undefined as Harness | undefined, api: undefined as ZoneApi | undefined }));

/** 在单设备替身上加一台客户端设备：快照里带上它，发给它播放区的命令与删除单独记下。 */
function withClient(server: FakeZoneServer): { api: ZoneApi; harness: Harness } {
  const base = server.zone();
  const harness: Harness = {
    server,
    clientCommands: [],
    deleted: [],
    client: {
      device: { ...server.device(), deviceId: CLIENT_DEVICE, zoneId: CLIENT_ZONE, name: "Symfonium", kind: "subsonic", online: false, lastSeenAt: Date.now() - 5 * 60_000 },
      zone: { ...base, zoneId: CLIENT_ZONE, deviceId: CLIENT_DEVICE, settings: { ...base.settings, leveling: "off", downmix: { enabled: false } } },
    },
  };
  const api: ZoneApi = {
    ...server.api,
    fetchSnapshot: async () => {
      const snapshot = await server.api.fetchSnapshot();
      const client = harness.client;
      return client === null ? snapshot : { ...snapshot, devices: [...snapshot.devices, client.device], zones: [...snapshot.zones, client.zone] };
    },
    sendZoneCommand: async (zoneId, command) => {
      const client = harness.client;
      if (zoneId !== CLIENT_ZONE || client === null) return server.api.sendZoneCommand(zoneId, command);
      harness.clientCommands.push(command);
      const settings = command.command === "set_settings" && command.settings.peq !== undefined ? { ...client.zone.settings, peq: { ...client.zone.settings.peq, enabled: command.settings.peq.enabled } } : client.zone.settings;
      client.zone = { ...client.zone, revision: client.zone.revision + 1, settings };
      return { serverTime: Date.now(), zone: client.zone, queueChange: null, targetZone: null, imported: null };
    },
    deleteDevice: async (deviceId) => {
      if (deviceId !== CLIENT_DEVICE) return server.api.deleteDevice(deviceId);
      harness.deleted.push(deviceId);
      harness.client = null;
    },
  };
  return { api, harness };
}

vi.mock("../features/player/zones/api", async (original) => ({
  ...(await original<typeof import("../features/player/zones/api")>()),
  browserZoneApi: new Proxy({} as ZoneApi, {
    get: (_target, key: keyof ZoneApi) => {
      if (zones.api === undefined) throw new Error("缺少播放区替身");
      return zones.api[key];
    },
  }),
}));

beforeEach(() => {
  const { api, harness } = withClient(fakeZoneServer());
  zones.api = api;
  zones.harness = harness;
  vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } });
  sessionStorage.clear();
  localStorage.clear();
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function setup() {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  return api;
}

describe("第三方客户端的设备", () => {
  it("只用键盘选中客户端设备：地址带 device，表单改它的播放区，不显示无缝与渐变", async () => {
    setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: "/settings/playback" });
    const target = await screen.findByRole("combobox", { name: "设置的设备" });
    expect(screen.getByRole("switch", { name: "无缝播放" })).toBeTruthy();
    target.focus();
    await user.selectOptions(target, "Symfonium（第三方客户端）");
    await waitFor(() => expect(history.location.search).toContain(`device=${CLIENT_DEVICE}`));
    expect(await screen.findByText(/这些设置作用于第三方客户端/)).toBeTruthy();
    expect(screen.getByText(/最近使用于5 分钟前/)).toBeTruthy();
    expect(screen.queryByRole("switch", { name: "无缝播放" })).toBeNull();
    expect(screen.queryByRole("switch", { name: "交叉渐变" })).toBeNull();
    const peq = screen.getByRole("switch", { name: "参数均衡" });
    peq.focus();
    await user.keyboard(" ");
    await waitFor(() => expect(zones.harness?.clientCommands.map((command) => command.command)).toEqual(["set_settings"]));
    expect(zones.harness?.server.commands.filter((command) => command.command === "set_settings")).toEqual([]);
    await waitFor(() => expect((screen.getByRole("switch", { name: "参数均衡" }) as HTMLInputElement).checked).toBe(true));
  });

  it("删除客户端设备：确认后删除，回到正在控制的设备", async () => {
    setup();
    const user = userEvent.setup();
    const { history } = renderApp({ path: `/settings/playback?device=${CLIENT_DEVICE}` });
    await user.click(await screen.findByRole("button", { name: "删除“Symfonium”" }));
    const dialog = within(await screen.findByRole("dialog", { name: "删除“Symfonium”？" }));
    expect(dialog.getByText(/下次连接时会重新登记/)).toBeTruthy();
    await user.click(dialog.getByRole("button", { name: "删除" }));
    await waitFor(() => expect(zones.harness?.deleted).toEqual([CLIENT_DEVICE]));
    await waitFor(() => expect(history.location.search).not.toContain("device="));
    expect(screen.queryByRole("combobox", { name: "设置的设备" })).toBeNull();
  });

  it("设备下拉只有正在控制的设备与客户端设备", async () => {
    setup();
    renderApp({ path: "/settings/playback" });
    const target = (await screen.findByRole("combobox", { name: "设置的设备" })) as HTMLSelectElement;
    await waitFor(() => expect([...target.options].map((option) => option.textContent)).toEqual(["正在控制：测试设备", "Symfonium（第三方客户端）"]));
  });
});
