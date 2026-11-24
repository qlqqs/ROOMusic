import { act, createEvent, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../../shared/api";
import { deferred } from "../../../test/deferred";
import { fakeZoneServer, testPlayerController } from "../../../test/fake-zones";
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
function viewDevice(target: "local" | "remote", lan?: { occupant: string | null; formatDeclared?: boolean }) {
  const server = fakeZoneServer();
  engine = testPlayerController(server).controller;
  const local = { ...server.device(), online: true };
  const remote = {
    ...local, deviceId: remoteDeviceId, zoneId: remoteZoneId, name: "客厅音箱",
    kind: lan === undefined ? "browser" : "upnp",
    upnp: lan === undefined ? null : { maxSampleRate: 48000, formatDeclared: lan.formatDeclared ?? false, occupiedBy: null },
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
