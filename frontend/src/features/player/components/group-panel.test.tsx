import { act, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../../shared/api";
import { deferred } from "../../../test/deferred";
import { fakeZoneServer, testPlayerController } from "../../../test/fake-zones";
import { groupOf, leaderOf, memberOf, slimDevice, slimDeviceId, slimZone } from "../../../test/group-data";
import type { PlayerController, PlayerSnapshot } from "../zones/controller";
import type { Device, Zone } from "../zones/model";
import { GroupDialog } from "./group-panel";

vi.mock("../player-provider", () => ({
  usePlayer: function useTestPlayer() { return { engine, snapshot }; },
  usePlayerSelector: function useTestPlayerSelector<T>(select: (current: PlayerSnapshot) => T) { return select(snapshot); },
}));

let engine: PlayerController;
let snapshot: PlayerSnapshot;

afterEach(() => {
  vi.restoreAllMocks();
});

const busyError = new ApiError({ kind: "http", status: 409, code: "device_busy", message: "小王正在使用这台设备", requestId: "group-test" });

/**
 * 主设备“客厅”的组：书房（同步中，微调 −20 ms）、厨房（固定音量，被占用）、一台看不到的成员（已掉线）。
 * 其余设备：卧室（空闲）、阁楼（小林在用）可以加入；阳台离线、车库已在别的组、局域网 UPnP 音箱与本机浏览器都不列出。
 * extraMembers 再往组里补几台在线成员（测组满）；volumeOf 替换控制器给出的每台音量（模拟拖动后的本地值）。
 */
function viewPanel(options: { extraMembers?: number; leaderId?: string; volumeOf?: PlayerController["volumeOf"] } = {}) {
  const server = fakeZoneServer();
  engine = testPlayerController(server).controller;
  if (options.volumeOf !== undefined) vi.spyOn(engine, "volumeOf").mockImplementation(options.volumeOf);
  const base = server.zone();
  const leaderZoneId = slimDevice(1, "客厅").zoneId;
  const hiddenId = slimDeviceId(99);
  const study = slimDevice(2, "书房", { volume: 0.6, syncDelayMs: -20, group: { zoneId: leaderZoneId, role: "member" } });
  const kitchen = slimDevice(3, "厨房", { volumeControllable: false, group: { zoneId: leaderZoneId, role: "member" } });
  const extra = Array.from({ length: options.extraMembers ?? 0 }, (_, index) => slimDevice(20 + index, `成员 ${index + 1}`, { group: { zoneId: leaderZoneId, role: "member" } }));
  const zone: Zone = slimZone(base, slimDevice(1, "客厅"), {
    group: groupOf([study, "active"], [kitchen, "busy"], [hiddenId, "dropped"], ...extra.map((device) => [device, "waiting"] as const)),
  });
  const leader = slimDevice(1, "客厅", { volume: 0.4, group: leaderOf(zone) });
  const otherZone = slimZone(base, slimDevice(8, "别处"));
  const others: Device[] = [
    slimDevice(4, "卧室"),
    slimDevice(5, "阁楼", { lan: { maxSampleRate: 48000, formatDeclared: true, occupiedBy: { userId: "00000000-0000-4000-8000-000000000009", displayName: "小林" }, unavailableReason: null } }),
    slimDevice(6, "阳台", { online: false }),
    slimDevice(7, "车库", { group: memberOf(otherZone) }),
    { ...slimDevice(9, "UPnP 音箱"), kind: "upnp" },
  ];
  const local = { ...server.device(), online: true };
  const devices = [leader, study, kitchen, ...extra, ...others];
  const occupantOf = (device: Device) => device.lan?.occupiedBy?.displayName ?? null;
  snapshot = {
    ...engine.getSnapshot(),
    zone,
    device: leader,
    isLocal: false,
    devices: [
      { device: local, zone: base, isLocal: true, isTarget: false, occupant: null },
      ...devices.map((device) => ({ device, zone: device === leader ? zone : slimZone(base, device), isLocal: false, isTarget: device === leader, occupant: occupantOf(device) })),
    ],
  };
  const onClose = vi.fn();
  render(<GroupDialog leaderDeviceId={options.leaderId ?? leader.deviceId} onClose={onClose} />);
  const panel = within(screen.getByRole("dialog", { name: options.leaderId === undefined ? "编组：客厅" : "编组" }));
  return { panel, zone, leader, study, kitchen, hiddenId, onClose };
}

const memberSection = (panel: ReturnType<typeof within>) => within(panel.getByRole("region", { name: "组内设备" }));
const candidateSection = (panel: ReturnType<typeof within>) => within(panel.getByRole("region", { name: "可以加入" }));
const delayOf = (panel: ReturnType<typeof within>, name: string) => panel.getByRole("spinbutton", { name: `“${name}”的同步微调（毫秒）` }) as HTMLInputElement;

describe("编组面板", () => {
  it("主设备在前标皇冠，成员带状态的读屏文字；看不到的成员只剩移出按钮", () => {
    const { panel } = viewPanel();
    const members = memberSection(panel).getAllByRole("listitem");
    expect(members.map((item) => item.textContent?.replace(/ms$/, ""))).toEqual([
      expect.stringContaining("主设备：客厅"),
      expect.stringContaining("同步中：书房"),
      expect.stringContaining("被占用：厨房"),
      expect.stringContaining("已掉线：看不到的设备"),
    ]);
    const [leaderRow, , , hiddenRow] = members.map((item) => within(item));
    expect(leaderRow?.queryByRole("button", { name: /移出组/ }), "主设备不能移出").toBeNull();
    expect(hiddenRow?.getByRole("button", { name: "把“看不到的设备”移出组" })).toBeTruthy();
    expect(hiddenRow?.queryByRole("slider")).toBeNull();
    expect(hiddenRow?.queryByRole("spinbutton")).toBeNull();
    expect(panel.getByText("毫秒级同步，适合不同房间；同一房间的左右声道请用一台播放器。")).toBeTruthy();
  });

  it("可以加入的只有在线、不在任何组里的其他 SlimProto 设备，别人在用的写出占用者", () => {
    const { panel } = viewPanel();
    const candidates = candidateSection(panel);
    expect(candidates.getAllByRole("button").map((button) => button.getAttribute("aria-label"))).toEqual(["加入“卧室”", "加入“阁楼”"]);
    expect(candidates.getByText("小林 正在使用")).toBeTruthy();
  });

  it("加入空闲设备直接发出，不接管", async () => {
    const user = userEvent.setup();
    const { panel, zone } = viewPanel();
    const add = vi.spyOn(engine, "addGroupMember").mockResolvedValue(undefined);
    await user.click(panel.getByRole("button", { name: "加入“卧室”" }));
    expect(add).toHaveBeenCalledExactlyOnceWith(zone.zoneId, slimDeviceId(4), false);
    expect(screen.queryByRole("dialog", { name: /^接管/ })).toBeNull();
  });

  it("加入别人在用的设备先确认接管：取消不发请求，确认后带 takeover", async () => {
    const user = userEvent.setup();
    const { panel, zone } = viewPanel();
    const add = vi.spyOn(engine, "addGroupMember").mockResolvedValue(undefined);
    await user.click(panel.getByRole("button", { name: "加入“阁楼”" }));
    const confirm = () => screen.getByRole("dialog", { name: "接管“阁楼”？" });
    expect(confirm().textContent).toContain("小林 正在使用这台设备。接管后对方的播放会暂停。");
    await user.click(within(confirm()).getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog", { name: "接管“阁楼”？" })).toBeNull();
    expect(add).not.toHaveBeenCalled();
    await user.click(panel.getByRole("button", { name: "加入“阁楼”" }));
    await user.click(within(confirm()).getByRole("button", { name: "接管" }));
    expect(add).toHaveBeenCalledExactlyOnceWith(zone.zoneId, slimDeviceId(5), true);
    expect(screen.queryByRole("dialog", { name: "接管“阁楼”？" })).toBeNull();
  });

  it("列表过期、加入时 409 device_busy：改为确认接管，确认后以 takeover 重试", async () => {
    const user = userEvent.setup();
    const { panel, zone } = viewPanel();
    const add = vi.spyOn(engine, "addGroupMember").mockRejectedValueOnce(busyError).mockResolvedValueOnce(undefined);
    await user.click(panel.getByRole("button", { name: "加入“卧室”" }));
    const confirm = await screen.findByRole("dialog", { name: "接管“卧室”？" });
    expect(confirm.textContent).toContain("小王正在使用这台设备");
    await user.click(within(confirm).getByRole("button", { name: "接管" }));
    expect(add.mock.calls).toEqual([[zone.zoneId, slimDeviceId(4), false], [zone.zoneId, slimDeviceId(4), true]]);
  });

  it("接管加入失败时关闭确认、在面板里提示", async () => {
    const user = userEvent.setup();
    const { panel } = viewPanel();
    vi.spyOn(engine, "addGroupMember").mockRejectedValue(new ApiError({ kind: "http", status: 409, code: "group_full", message: "组已满", requestId: "group-test" }));
    await user.click(panel.getByRole("button", { name: "加入“阁楼”" }));
    await user.click(within(screen.getByRole("dialog", { name: "接管“阁楼”？" })).getByRole("button", { name: "接管" }));
    expect(await panel.findByText("一组最多 8 台设备。")).toBeTruthy();
    expect(screen.queryByRole("dialog", { name: "接管“阁楼”？" })).toBeNull();
  });

  it.each([
    ["group_full", "一组最多 8 台设备。"],
    ["device_grouped", "这台设备已在组里。"],
    ["group_unsupported", "先选用这台设备，再加入其他设备。"],
    ["zone_offline", "主设备已离线。"],
  ])("加入被拒为 %s 时就近提示", async (code, text) => {
    const user = userEvent.setup();
    const { panel } = viewPanel();
    vi.spyOn(engine, "addGroupMember").mockRejectedValue(new ApiError({ kind: "http", status: 409, code, message: code, requestId: "group-test" }));
    await user.click(panel.getByRole("button", { name: "加入“卧室”" }));
    expect(await panel.findByText(text)).toBeTruthy();
  });

  it("加入请求进行中按钮标为忙，不重复发出", async () => {
    const user = userEvent.setup();
    const { panel } = viewPanel();
    const pending = deferred<void>();
    const add = vi.spyOn(engine, "addGroupMember").mockReturnValue(pending.promise);
    const button = panel.getByRole("button", { name: "加入“卧室”" });
    await user.click(button);
    expect(button.getAttribute("aria-busy")).toBe("true");
    await user.click(button);
    expect(add).toHaveBeenCalledTimes(1);
    await act(async () => pending.resolve(undefined));
    expect(button.getAttribute("aria-busy")).toBeNull();
  });

  it("组满 8 台时不再列出可以加入的设备", () => {
    const { panel } = viewPanel({ extraMembers: 4 });
    const candidates = candidateSection(panel);
    expect(candidates.queryAllByRole("button")).toHaveLength(0);
    expect(candidates.getByText("一组最多 8 台设备。")).toBeTruthy();
  });

  it("移出成员发出主设备播放区与成员设备", async () => {
    const user = userEvent.setup();
    const { panel, zone, study, hiddenId } = viewPanel();
    const remove = vi.spyOn(engine, "removeGroupMember").mockResolvedValue(undefined);
    await user.click(panel.getByRole("button", { name: "把“书房”移出组" }));
    await user.click(panel.getByRole("button", { name: "把“看不到的设备”移出组" }));
    expect(remove.mock.calls).toEqual([[zone.zoneId, study.deviceId], [zone.zoneId, hiddenId]]);
  });

  it("每台的音量滑块调这一台，固定音量的设备禁用并读作“固定音量”", () => {
    const { panel, study, kitchen } = viewPanel();
    const set = vi.spyOn(engine, "setDeviceVolume").mockImplementation(() => undefined);
    const leaderSlider = panel.getByRole<HTMLInputElement>("slider", { name: "“客厅”的音量" });
    const studySlider = panel.getByRole<HTMLInputElement>("slider", { name: "“书房”的音量" });
    const kitchenSlider = panel.getByRole<HTMLInputElement>("slider", { name: "“厨房”的音量" });
    expect([leaderSlider.value, studySlider.value]).toEqual(["0.4", "0.6"]);
    expect(studySlider.getAttribute("aria-valuetext")).toBe("60%");
    expect(kitchenSlider.disabled).toBe(true);
    expect(kitchenSlider.getAttribute("aria-valuetext")).toBe("固定音量");
    fireEvent.change(studySlider, { target: { value: "0.25" } });
    expect(set).toHaveBeenCalledExactlyOnceWith(study.deviceId, 0.25);
    expect(set).not.toHaveBeenCalledWith(kitchen.deviceId, expect.anything());
  });

  it("滑块显示控制器给出的本地值与静音", () => {
    const { panel } = viewPanel({ volumeOf: (device) => (device.name === "书房" ? { volume: 0.3, muted: true } : { volume: device.volume, muted: device.muted }) });
    const slider = panel.getByRole<HTMLInputElement>("slider", { name: "“书房”的音量" });
    expect(slider.value).toBe("0.3");
    expect(slider.getAttribute("aria-valuetext")).toBe("已静音，30%");
    expect(slider.hasAttribute("data-muted")).toBe(true);
  });

  describe("同步微调", () => {
    it("显示已保存的毫秒值；回车保存新值", async () => {
      const { panel, study } = viewPanel();
      const save = vi.spyOn(engine, "setSyncDelay").mockResolvedValue(undefined);
      const input = delayOf(panel, "书房");
      expect(input.value).toBe("-20");
      expect(delayOf(panel, "客厅").value).toBe("0");
      fireEvent.change(input, { target: { value: "35" } });
      fireEvent.keyDown(input, { key: "Enter" });
      expect(save).toHaveBeenCalledExactlyOnceWith(study.deviceId, 35);
      expect(input.getAttribute("aria-busy")).toBe("true");
      // 保存进行中失焦不重复发出。
      fireEvent.blur(input);
      expect(save).toHaveBeenCalledTimes(1);
      await act(async () => undefined);
      expect(input.getAttribute("aria-busy")).toBeNull();
    });

    it("离开输入框时保存；和已保存的值相同不发请求", () => {
      const { panel, leader } = viewPanel();
      const save = vi.spyOn(engine, "setSyncDelay").mockResolvedValue(undefined);
      const input = delayOf(panel, "客厅");
      fireEvent.change(input, { target: { value: "0" } });
      fireEvent.blur(input);
      expect(save).not.toHaveBeenCalled();
      fireEvent.change(input, { target: { value: "-500" } });
      fireEvent.blur(input);
      expect(save).toHaveBeenCalledExactlyOnceWith(leader.deviceId, -500);
    });

    it("Esc 撤回未保存的输入，不关闭面板", () => {
      const { panel, onClose } = viewPanel();
      const save = vi.spyOn(engine, "setSyncDelay").mockResolvedValue(undefined);
      const input = delayOf(panel, "书房");
      fireEvent.change(input, { target: { value: "120" } });
      const escape = fireEvent.keyDown(input, { key: "Escape" });
      expect(escape, "有未保存的输入时 Esc 被拦下").toBe(false);
      expect(input.value).toBe("-20");
      fireEvent.blur(input);
      expect(save).not.toHaveBeenCalled();
      expect(onClose).not.toHaveBeenCalled();
      // 没有未保存的输入时 Esc 不拦，交给对话框关闭。
      expect(fireEvent.keyDown(input, { key: "Escape" })).toBe(true);
    });

    it.each(["501", "-501", "1.5", "", " "])("超出 −500…500 或不是整数（%j）时内联报错、不发请求，改动后清掉报错", (value) => {
      const { panel } = viewPanel();
      const save = vi.spyOn(engine, "setSyncDelay").mockResolvedValue(undefined);
      const input = delayOf(panel, "书房");
      fireEvent.change(input, { target: { value } });
      fireEvent.keyDown(input, { key: "Enter" });
      expect(save).not.toHaveBeenCalled();
      expect(input.getAttribute("aria-invalid")).toBe("true");
      expect(panel.getByText("取值 −500 到 500 毫秒。")).toBeTruthy();
      fireEvent.change(input, { target: { value: "10" } });
      expect(input.hasAttribute("aria-invalid")).toBe(false);
      expect(panel.queryByText("取值 −500 到 500 毫秒。")).toBeNull();
    });

    it("保存失败时在这一台下面提示，保留输入", async () => {
      const { panel } = viewPanel();
      vi.spyOn(engine, "setSyncDelay").mockRejectedValue(new ApiError({ kind: "http", status: 400, code: "invalid_input", message: "invalid", requestId: "group-test" }));
      const input = delayOf(panel, "书房");
      fireEvent.change(input, { target: { value: "40" } });
      fireEvent.keyDown(input, { key: "Enter" });
      const studyRow = within(panel.getAllByRole("listitem")[1] as HTMLElement);
      expect(await studyRow.findByRole("alert")).toBeTruthy();
      expect(input.value).toBe("40");
    });

    it("固定音量的设备也能调同步微调", () => {
      const { panel, kitchen } = viewPanel();
      const save = vi.spyOn(engine, "setSyncDelay").mockResolvedValue(undefined);
      const input = delayOf(panel, "厨房");
      expect(input.disabled).toBe(false);
      fireEvent.change(input, { target: { value: "-80" } });
      fireEvent.keyDown(input, { key: "Enter" });
      expect(save).toHaveBeenCalledExactlyOnceWith(kitchen.deviceId, -80);
    });
  });

  it("主设备已不在列表里时说明不可用，不列出成员与候选", () => {
    const { panel } = viewPanel({ leaderId: slimDeviceId(77) });
    expect(panel.getByText("这台设备已不可用。")).toBeTruthy();
    expect(panel.queryByRole("region", { name: "组内设备" })).toBeNull();
  });

  it("关闭按钮交给调用方", async () => {
    const user = userEvent.setup();
    const { panel, onClose } = viewPanel();
    await user.click(panel.getByRole("button", { name: "关闭" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
