import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { jukeboxZoneOptions } from "./routes/account-page";
import type { Device, PlaybackDeviceRow, Zone } from "../features/player";
import { decode as d } from "../shared/api";
import { installFakeApi, installFakeSession } from "../test/fake-api";
import { FakeAudio } from "../test/fake-audio";
import { fakeZoneServer, type FakeZoneServer } from "../test/fake-zones";
import { jsonResponse } from "../test/http";
import { renderApp } from "../test/render-app";

// 设置 › 账号 › 第三方客户端的“点唱机控制”：完整应用，播放区服务端是替身（一台“测试设备”）。

const zones = vi.hoisted(() => ({ server: undefined as FakeZoneServer | undefined }));
vi.mock("../features/player/zones/api", async (original) => {
  const { delegatingZoneApi } = await import("../test/fake-zones");
  return {
    ...(await original<typeof import("../features/player/zones/api")>()),
    browserZoneApi: delegatingZoneApi(() => {
      if (zones.server === undefined) throw new Error("缺少播放区替身");
      return zones.server;
    }),
  };
});

beforeEach(() => {
  zones.server = fakeZoneServer();
  vi.stubGlobal("Audio", class { constructor() { return new FakeAudio(); } });
  sessionStorage.clear();
  localStorage.clear();
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const connectionPath = "/api/v1/me/subsonic";
const putBody = d.defineDecoder(d.object({ jukebox_zone_id: d.nullable(d.string()) }));

function setup(options: { fail?: boolean } = {}) {
  const api = installFakeApi();
  installFakeSession(api, { current: "listener" });
  const state = { zone: null as string | null };
  const connection = () => ({ enabled: true, ready: true, username: "listener", jukebox_zone_id: state.zone });
  api.on("GET", connectionPath, () => jsonResponse(200, connection()));
  api.on("PUT", connectionPath, ({ body }) => {
    if (options.fail === true) return jsonResponse(404, { error: { code: "not_found", message: "不存在" }, request_id: "r" });
    state.zone = putBody(body).jukebox_zone_id;
    return jsonResponse(200, connection());
  });
  return { api, state };
}

const jukeboxSelect = async () => {
  const group = within((await screen.findByRole("heading", { level: 2, name: "第三方客户端" })).closest("section") as HTMLElement);
  const select = await group.findByRole("combobox", { name: "点唱机控制" });
  await waitFor(() => expect(select.hasAttribute("disabled")).toBe(false));
  return { group, select: select as HTMLSelectElement };
};

describe("点唱机控制", () => {
  it("没选过时显示“最近播放过的播放区”；只用键盘选一台设备即保存，再改回自动保存 null", async () => {
    const user = userEvent.setup();
    const { api, state } = setup();
    renderApp({ path: "/settings/account" });
    const { select } = await jukeboxSelect();
    expect(select.selectedOptions[0]?.textContent).toBe("最近播放过的播放区");
    expect(select.getAttribute("aria-describedby")).not.toBeNull();
    const zoneId = zones.server?.zone().zoneId ?? "";
    select.focus();
    await user.selectOptions(select, "测试设备");
    await waitFor(() => expect(state.zone).toBe(zoneId));
    expect(select.value).toBe(zoneId);
    await user.selectOptions(select, "最近播放过的播放区");
    await waitFor(() => expect(state.zone).toBeNull());
    expect(api.requestsTo("PUT", connectionPath).map((request) => putBody(request.body).jukebox_zone_id)).toEqual([zoneId, null]);
  });

  it("保存失败：说明原因，下拉回到已保存的值", async () => {
    const user = userEvent.setup();
    setup({ fail: true });
    renderApp({ path: "/settings/account" });
    const { group, select } = await jukeboxSelect();
    await user.selectOptions(select, "测试设备");
    expect(await group.findByText("这个播放区已经不存在。")).toBeTruthy();
    expect(select.selectedOptions[0]?.textContent).toBe("最近播放过的播放区");
  });
});

describe("点唱机可选的播放区", () => {
  const device = (id: string, name: string, group: Device["group"] = null): Device => ({
    deviceId: `device-${id}`, zoneId: `zone-${id}`, name, kind: "slimproto", online: true, lastSeenAt: 0, volume: 1, muted: false,
    volumeControllable: true, outputSessionId: null, lan: null, group, syncDelayMs: null,
  });
  const row = (item: Device, members = 0): PlaybackDeviceRow => ({
    device: item, isLocal: false, isTarget: false, occupant: null,
    zone: { group: members === 0 ? null : { members: Array.from({ length: members }, (_, i) => ({ deviceId: `m${i}`, state: "active" as const })) } } as Zone,
  });

  it("编组按主设备列出并带台数，加入别人编组的成员不单列", () => {
    const options = jukeboxZoneOptions([
      row(device("a", "客厅", { zoneId: "zone-a", role: "leader" }), 1),
      row(device("b", "书房", { zoneId: "zone-a", role: "member" })),
      row(device("c", "卧室")),
    ]);
    expect(options).toEqual([{ id: "zone-a", label: "客厅（编组 2 台）" }, { id: "zone-c", label: "卧室" }]);
  });
});
