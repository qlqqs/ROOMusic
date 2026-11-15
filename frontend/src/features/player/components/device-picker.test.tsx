import { act, createEvent, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { fakeZoneServer, testPlayerController } from "../../../test/fake-zones";
import type { PlayerController, PlayerSnapshot } from "../zones/controller";
import { DeviceButton } from "./device-picker";

vi.mock("../player-provider", () => ({
  usePlayer: function useTestPlayer() { return { engine, snapshot }; },
  usePlayerSelector: function useTestPlayerSelector<T>(select: (current: PlayerSnapshot) => T) { return select(snapshot); },
}));

let engine: PlayerController;
let snapshot: PlayerSnapshot;

afterEach(() => {
  vi.useRealTimers();
});

/** 控制本机或别的设备（客厅音箱）时的设备按钮。 */
function viewDevice(target: "local" | "remote") {
  const server = fakeZoneServer();
  engine = testPlayerController(server).controller;
  const local = server.device();
  const remote = { ...local, deviceId: "00000000-0000-4000-8000-00000000d002", zoneId: "00000000-0000-4000-8000-00000000c002", name: "客厅音箱", online: true };
  snapshot = {
    ...engine.getSnapshot(),
    isLocal: target === "local",
    device: target === "local" ? local : remote,
    devices: [
      { device: local, zone: null, isLocal: true, isTarget: target === "local" },
      { device: remote, zone: null, isLocal: false, isTarget: target === "remote" },
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
