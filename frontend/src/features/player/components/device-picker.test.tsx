import { act, createEvent, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../../shared/api";
import { deferred } from "../../../test/deferred";
import { fakeZoneServer, testPlayerController } from "../../../test/fake-zones";
import { groupOf, leaderOf, memberOf, slimDevice, slimZone } from "../../../test/group-data";
import { playerTrack } from "../../../test/player-data";
import type { PlayerController, PlayerSnapshot } from "../zones/controller";
import { DeviceButton } from "./device-picker";

vi.mock("../player-provider", () => ({
  usePlayer: function useTestPlayer() { return { engine, snapshot }; },
  usePlayerSelector: function useTestPlayerSelector<T>(select: (current: PlayerSnapshot) => T) { return select(snapshot); },
}));

let engine: PlayerController;
let snapshot: PlayerSnapshot;
const remoteDeviceId = "00000000-0000-4000-8000-00000000d002";
const remoteZoneId = "00000000-0000-4000-8000-00000000c002";

afterEach(() => {
  vi.restoreAllMocks();
  vi.useRealTimers();
});

/** 控制本机或别的设备（客厅音箱）时的设备按钮。 */
function viewDevice(target: "local" | "remote", lan?: { occupant: string | null; formatDeclared?: boolean; kind?: "upnp" | "cast" | "airplay"; unavailableReason?: string }) {
  const server = fakeZoneServer();
  engine = testPlayerController(server).controller;
  const local = { ...server.device(), online: true };
  const remote = {
    ...local, deviceId: remoteDeviceId, zoneId: remoteZoneId, name: "客厅音箱",
    kind: lan === undefined ? "browser" : (lan.kind ?? "upnp"),
    lan: lan === undefined ? null : { maxSampleRate: 48000, formatDeclared: lan.formatDeclared ?? false, occupiedBy: null, unavailableReason: lan.unavailableReason ?? null },
  };
  const source = { ...server.zone(), current: { entryId: "entry-1", track: playerTrack("示例曲目") } };
  snapshot = {
    ...engine.getSnapshot(),
    isLocal: target === "local",
    device: target === "local" ? local : remote,
    zone: target === "local" ? source : null,
    devices: [
      { device: local, zone: null, isLocal: true, isTarget: target === "local", occupant: null },
      { device: remote, zone: null, isLocal: false, isTarget: target === "remote", occupant: lan?.occupant ?? null },
    ],
  };
  return render(<DeviceButton className="where" />);
}

function hover(element: HTMLElement) {
  const over = createEvent.pointerOver(element);
  Object.defineProperty(over, "pointerType", { value: "mouse" });
  fireEvent(element, over);
}

describe("播放设备按钮", () => {
  it("控制本机时是没有文字的普通图标，名称“播放设备”，没有远端标记", () => {
    const { container } = viewDevice("local");
    const button = screen.getByRole("button", { name: "播放设备" });
    expect(button.textContent).toBe("");
    const wrapper = container.firstElementChild as HTMLElement;
    expect(wrapper.hasAttribute("data-remote")).toBe(false);
    expect(wrapper.classList.contains("where")).toBe(true);
  });

  it("控制别的设备时只有图标、带远端标记，读屏名与悬停提示写“正在控制：设备名”", () => {
    vi.useFakeTimers();
    const { container } = viewDevice("remote");
    const button = screen.getByRole("button", { name: "正在控制：客厅音箱" });
    // 设备名不是可见文字（只在名称与提示里），按钮里只有图标。
    expect(button.textContent).toBe("");
    expect((container.firstElementChild as HTMLElement).hasAttribute("data-remote")).toBe(true);
    expect(button.hasAttribute("title")).toBe(false);
    hover(button);
    act(() => vi.advanceTimersByTime(300));
    expect(screen.getByText("正在控制：客厅音箱").getAttribute("aria-hidden")).toBe("true");
  });

  it("点按打开设备列表；按钮在视口上半部时弹层在按钮下方，下半部时在上方", async () => {
    const user = userEvent.setup();
    viewDevice("remote");
    const button = screen.getByRole("button", { name: "正在控制：客厅音箱" });
    const rect = (top: number) => ({ top, bottom: top + 32, left: 900, right: 932, width: 32, height: 32, x: 900, y: top, toJSON: () => ({}) }) as DOMRect;
    vi.spyOn(button, "getBoundingClientRect").mockReturnValue(rect(18));
    await user.click(button);
    let dialog = screen.getByRole("dialog", { name: "播放设备" });
    expect(within(dialog).getByRole("button", { name: /客厅音箱/, pressed: true })).toBeTruthy();
    let content = dialog.firstElementChild as HTMLElement;
    expect(content.hasAttribute("data-below")).toBe(true);
    expect(content.style.getPropertyValue("--anchor-bottom")).toBe("50px");
    await user.click(within(dialog).getByRole("button", { name: "关闭" }));
    vi.spyOn(button, "getBoundingClientRect").mockReturnValue(rect(window.innerHeight - 50));
    await user.click(button);
    dialog = screen.getByRole("dialog", { name: "播放设备" });
    content = dialog.firstElementChild as HTMLElement;
    expect(content.hasAttribute("data-below")).toBe(false);
  });
});

describe("播放设备列表的局域网操作", () => {
  async function open(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole("button", { name: "播放设备" }));
    return within(screen.getByRole("dialog", { name: "播放设备" }));
  }

  const remoteRow = (picker: ReturnType<typeof within>) => picker.getByRole("button", { name: /客厅音箱/ });
  const remoteActions = (picker: ReturnType<typeof within>) => within(picker.getByRole("group", { name: "客厅音箱的操作" }));
  const takeoverDialog = () => screen.getByRole("dialog", { name: "接管“客厅音箱”？" });

  it("局域网设备行显示独立的音箱图标", async () => {
    const user = userEvent.setup();
    viewDevice("local", { occupant: null });
    const picker = await open(user);
    expect(remoteRow(picker).querySelector("svg.lucide-speaker"), "应在设备名称旁显示音箱图标").not.toBeNull();
  });

  it("标出局域网和占用者，取消接管不发请求，确认后才激活", async () => {
    const user = userEvent.setup();
    viewDevice("local", { occupant: "小林" });
    const activate = vi.spyOn(engine, "activateDevice").mockResolvedValue(undefined);
    const picker = await open(user);
    expect(within(remoteRow(picker)).getByText("局域网")).toBeTruthy();
    expect(within(remoteRow(picker)).getByText("小林 正在使用")).toBeTruthy();
    await user.click(remoteRow(picker));
    expect(takeoverDialog().textContent).toContain("小林 正在使用这台设备");
    expect(activate).not.toHaveBeenCalled();
    await user.click(within(takeoverDialog()).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog", { name: "接管“客厅音箱”？" })).toBeNull();
    expect(activate).not.toHaveBeenCalled();
    await user.click(remoteRow(picker));
    await user.click(within(takeoverDialog()).getByRole("button", { name: "接管" }));
    expect(activate).toHaveBeenCalledExactlyOnceWith(remoteDeviceId, true);
    expect(screen.queryByRole("dialog", { name: "播放设备" })).toBeNull();
  });

  it("点选空闲音箱时直接激活", async () => {
    const user = userEvent.setup();
    viewDevice("local", { occupant: null });
    const activate = vi.spyOn(engine, "activateDevice").mockResolvedValue(undefined);
    await user.click(remoteRow(await open(user)));
    expect(activate).toHaveBeenCalledExactlyOnceWith(remoteDeviceId, false);
    expect(screen.queryByRole("dialog", { name: "播放设备" })).toBeNull();
  });

  it("空闲列表过期时，409 device_busy 改为提示确认接管", async () => {
    const user = userEvent.setup();
    viewDevice("local", { occupant: null });
    const activate = vi.spyOn(engine, "activateDevice").mockRejectedValueOnce(new ApiError({
      kind: "http", status: 409, code: "device_busy", message: "小林正在使用这台设备", requestId: "busy-test",
    })).mockResolvedValueOnce(undefined);
    await user.click(remoteRow(await open(user)));
    expect(activate).toHaveBeenCalledExactlyOnceWith(remoteDeviceId, false);
    expect(takeoverDialog().textContent).toContain("小林正在使用这台设备");
    await user.click(within(takeoverDialog()).getByRole("button", { name: "接管" }));
    expect(activate.mock.calls).toEqual([[remoteDeviceId, false], [remoteDeviceId, true]]);
  });

  it("转到局域网设备时等待激活成功，再从原播放区转移", async () => {
    const user = userEvent.setup();
    viewDevice("local", { occupant: null });
    const activation = deferred<void>();
    const activate = vi.spyOn(engine, "activateDevice").mockReturnValue(activation.promise);
    const transfer = vi.spyOn(engine, "transfer").mockResolvedValue(undefined);
    const picker = await open(user);
    await user.click(remoteActions(picker).getByRole("button", { name: "转到这台设备" }));
    expect(activate).toHaveBeenCalledExactlyOnceWith(remoteDeviceId, false);
    expect(transfer).not.toHaveBeenCalled();
    await act(async () => activation.resolve(undefined));
    expect(transfer).toHaveBeenCalledExactlyOnceWith(snapshot.zone?.zoneId, remoteZoneId);
  });

  it("采样率列出四档，格式未声明时说明，取消不写入，保存写入选中值", async () => {
    const user = userEvent.setup();
    viewDevice("local", { occupant: null });
    const save = vi.spyOn(engine, "setDeviceSampleRate").mockResolvedValue(undefined);
    const opener = remoteActions(await open(user)).getByRole("button", { name: "采样率上限" });
    const dialog = () => within(screen.getByRole("dialog", { name: "“客厅音箱”的采样率上限" }));
    await user.click(opener);
    expect(dialog().getByText(/这台设备没有说明它支持的格式/)).toBeTruthy();
    expect(dialog().getAllByRole("radio")).toHaveLength(4);
    for (const rate of ["44.1 kHz", "48 kHz", "96 kHz", "192 kHz"]) expect(dialog().getByRole("radio", { name: rate })).toBeTruthy();
    expect(dialog().getByRole<HTMLInputElement>("radio", { name: "48 kHz" }).checked).toBe(true);
    await user.click(dialog().getByRole("radio", { name: "96 kHz" }));
    await user.click(dialog().getByRole("button", { name: "取消" }));
    expect(save).not.toHaveBeenCalled();
    await user.click(opener);
    expect(dialog().getByRole<HTMLInputElement>("radio", { name: "48 kHz" }).checked).toBe(true);
    await user.click(dialog().getByRole("radio", { name: "192 kHz" }));
    await user.click(dialog().getByRole("button", { name: "保存" }));
    expect(save).toHaveBeenCalledExactlyOnceWith(remoteDeviceId, 192000);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "“客厅音箱”的采样率上限" })).toBeNull());
  });

  it("Google Cast 设备标出类型，没有成员自己的采样率上限", async () => {
    const user = userEvent.setup();
    viewDevice("local", { occupant: null, kind: "cast" });
    await user.click(screen.getByRole("button", { name: "播放设备" }));
    const row = within(screen.getByRole("group", { name: "客厅音箱的操作" }));
    expect(screen.getByText("Google Cast")).toBeTruthy();
    expect(row.queryByRole("button", { name: "采样率上限" })).toBeNull();
  });

  it("AirPlay 设备不可用时显示原因，不能点选", async () => {
    const user = userEvent.setup();
    viewDevice("local", { occupant: null, kind: "airplay", unavailableReason: "精确对时不可用" });
    await user.click(screen.getByRole("button", { name: "播放设备" }));
    expect(screen.getByText("AirPlay")).toBeTruthy();
    expect(screen.getByText("精确对时不可用")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /客厅音箱/ })).toBeNull();
  });

  it("浏览器行保留直接控制与转移，没有局域网操作", async () => {
    const user = userEvent.setup();
    viewDevice("local");
    const activate = vi.spyOn(engine, "activateDevice").mockResolvedValue(undefined);
    const select = vi.spyOn(engine, "setControlTarget").mockImplementation(() => undefined);
    const transfer = vi.spyOn(engine, "transfer").mockResolvedValue(undefined);
    const picker = await open(user);
    expect(within(remoteRow(picker)).queryByText("局域网")).toBeNull();
    expect(remoteActions(picker).queryByRole("button", { name: "采样率上限" })).toBeNull();
    await user.click(remoteRow(picker));
    expect(select).toHaveBeenCalledExactlyOnceWith(remoteZoneId);
    await user.click(remoteActions(await open(user)).getByRole("button", { name: "转到这台设备" }));
    expect(transfer).toHaveBeenCalledExactlyOnceWith(snapshot.zone?.zoneId, remoteZoneId);
    expect(activate).not.toHaveBeenCalled();
  });
});

describe("播放设备列表的编组", () => {
  /**
   * 本机之外有三台在线的 SlimProto（客厅、书房、卧室）与一台离线的（阳台）。grouped 时客厅是主设备、书房是成员；
   * target 是正在控制的设备（null 为本机）。
   */
  function viewGroups(options: { target: "客厅" | "卧室" | null; grouped: boolean; leaderVisible?: boolean }) {
    const server = fakeZoneServer();
    engine = testPlayerController(server).controller;
    const local = { ...server.device(), online: true };
    const base = { ...server.zone(), current: { entryId: "entry-1", track: playerTrack("示例曲目") } };
    const leaderZone = slimZone(base, slimDevice(1, "客厅"), { group: options.grouped ? groupOf([slimDevice(2, "书房"), "active"]) : null });
    const leader = slimDevice(1, "客厅", { group: options.grouped ? leaderOf(leaderZone) : null });
    const member = slimDevice(2, "书房", { group: options.grouped ? memberOf(leaderZone) : null });
    const bedroom = slimDevice(3, "卧室");
    const balcony = slimDevice(4, "阳台", { online: false });
    const devices = [leader, member, bedroom, balcony];
    const zoneOf = (device: typeof leader) => (device === leader ? leaderZone : slimZone(server.zone(), device));
    const targetDevice = devices.find((device) => device.name === options.target) ?? null;
    snapshot = {
      ...engine.getSnapshot(),
      isLocal: targetDevice === null,
      device: targetDevice ?? local,
      zone: targetDevice === null ? base : zoneOf(targetDevice),
      devices: [
        { device: local, zone: base, isLocal: true, isTarget: targetDevice === null, occupant: null },
        ...devices.filter((device) => options.leaderVisible !== false || device !== leader)
          .map((device) => ({ device, zone: zoneOf(device), isLocal: false, isTarget: device === targetDevice, occupant: null })),
      ],
    };
    render(<DeviceButton />);
    return { leader, member, leaderZone };
  }

  async function open(user: ReturnType<typeof userEvent.setup>) {
    await user.click(screen.getByRole("button", { name: /^(播放设备|正在控制：)/ }));
    return within(screen.getByRole("dialog", { name: "播放设备" }));
  }
  const actions = (picker: ReturnType<typeof within>, name: string) => within(picker.getByRole("group", { name: `${name}的操作` }));

  it("只有正在控制的在线 SlimProto 设备有“编组”按钮，没有组时不强调", async () => {
    const user = userEvent.setup();
    viewGroups({ target: "卧室", grouped: false });
    const picker = await open(user);
    const button = actions(picker, "卧室").getByRole("button", { name: "编组" });
    expect(button.closest("[data-grouped]")).toBeNull();
    for (const name of ["客厅", "书房", "阳台", "测试设备"]) expect(actions(picker, name).queryByRole("button", { name: /^编组/ }), name).toBeNull();
  });

  it("主设备即使不是控制目标也有“编组”按钮，读屏名写出台数并加强调", async () => {
    const user = userEvent.setup();
    viewGroups({ target: null, grouped: true });
    const picker = await open(user);
    const button = actions(picker, "客厅").getByRole("button", { name: "编组（2 台）" });
    expect(button.closest("[data-grouped]")).not.toBeNull();
    expect(actions(picker, "卧室").queryByRole("button", { name: /^编组/ })).toBeNull();
  });

  it("成员行写出在谁的组里、不能单独选用或转过去，只能移出组", async () => {
    const user = userEvent.setup();
    const { leaderZone, member } = viewGroups({ target: null, grouped: true });
    const remove = vi.spyOn(engine, "removeGroupMember").mockResolvedValue(undefined);
    const select = vi.spyOn(engine, "setControlTarget").mockImplementation(() => undefined);
    const activate = vi.spyOn(engine, "activateDevice").mockResolvedValue(undefined);
    const picker = await open(user);
    expect(picker.getByText("在「客厅」的组里")).toBeTruthy();
    expect(picker.queryByRole("button", { name: /书房/ }), "成员行不是可点选的按钮").toBeNull();
    const memberActions = actions(picker, "书房");
    expect(memberActions.queryByRole("button", { name: "转到这台设备" })).toBeNull();
    expect(memberActions.queryByRole("button", { name: /^编组/ })).toBeNull();
    await user.click(picker.getByText("在「客厅」的组里"));
    expect(select).not.toHaveBeenCalled();
    expect(activate).not.toHaveBeenCalled();
    await user.click(memberActions.getByRole("button", { name: "移出组" }));
    expect(remove).toHaveBeenCalledExactlyOnceWith(leaderZone.zoneId, member.deviceId);
    // 不是成员的设备照常可以转过去。
    expect(actions(picker, "卧室").getByRole("button", { name: "转到这台设备" })).toBeTruthy();
  });

  it("主设备不在列表里时，成员行写“另一台设备”", async () => {
    const user = userEvent.setup();
    viewGroups({ target: null, grouped: true, leaderVisible: false });
    const picker = await open(user);
    expect(picker.getByText("在「另一台设备」的组里")).toBeTruthy();
  });

  it("移出组失败时在列表里提示", async () => {
    const user = userEvent.setup();
    viewGroups({ target: null, grouped: true });
    vi.spyOn(engine, "removeGroupMember").mockRejectedValue(new ApiError({ kind: "http", status: 409, code: "zone_offline", message: "离线", requestId: "group-test" }));
    const picker = await open(user);
    await user.click(actions(picker, "书房").getByRole("button", { name: "移出组" }));
    expect(await picker.findByText("那台设备已离线。")).toBeTruthy();
  });

  it("点“编组”打开主设备的编组面板，关闭后回到设备列表", async () => {
    const user = userEvent.setup();
    viewGroups({ target: "客厅", grouped: true });
    const picker = await open(user);
    await user.click(actions(picker, "客厅").getByRole("button", { name: "编组（2 台）" }));
    const panel = screen.getByRole("dialog", { name: "编组：客厅" });
    await user.click(within(panel).getByRole("button", { name: "关闭" }));
    expect(screen.queryByRole("dialog", { name: "编组：客厅" })).toBeNull();
    expect(screen.getByRole("dialog", { name: "播放设备" })).toBeTruthy();
  });
});
