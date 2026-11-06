import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { MenuButton } from "./menu-button";

describe("菜单按钮键盘操作", () => {
  it("下箭头打开并跳过禁用项，方向键循环、Home/End、Esc 恢复焦点", async () => {
    const user = userEvent.setup();
    render(<MenuButton label="更多" actions={[{ id: "1", label: "下一首播放", onSelect: vi.fn() }, { id: "2", label: "不可用", disabled: true, onSelect: vi.fn() }, { id: "3", label: "加入队列", onSelect: vi.fn() }]} />);
    const trigger = screen.getByRole("button", { name: "更多" });
    trigger.focus();
    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "下一首播放" }));
    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "加入队列" }));
    await user.keyboard("{ArrowDown}{End}{Home}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "下一首播放" }));
    await user.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(trigger);
    expect(trigger.getAttribute("aria-expanded")).toBe("false");
  });
  it.each(["{Enter}", " "])("%s 打开，回车执行后关闭并回到触发按钮", async (key) => {
    const user = userEvent.setup();
    const select = vi.fn();
    render(<MenuButton label="更多" actions={[{ id: "1", label: "加入队列", onSelect: select }]} />);
    const trigger = screen.getByRole("button", { name: "更多" });
    trigger.focus();
    await user.keyboard(key);
    expect(screen.getByRole("menu", { name: "更多" })).toBeTruthy();
    await user.keyboard("{Enter}");
    expect(select).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });
  it("上箭头打开时聚焦末项，Tab 离开而不是困在菜单中", async () => {
    const user = userEvent.setup();
    render(<><MenuButton label="更多" actions={[{ id: "1", label: "下一首播放", onSelect: vi.fn() }, { id: "2", label: "加入队列", onSelect: vi.fn() }]} /><button>后续操作</button></>);
    screen.getByRole("button", { name: "更多" }).focus();
    await user.keyboard("{ArrowUp}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "加入队列" }));
    await user.tab();
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "后续操作" }));
  });
});

describe("菜单浮层与图标", () => {
  it("placement=above 时用底边定位到触发器上方，限制高度以免越出视口", async () => {
    render(<MenuButton label="更多" placement="above" actions={[{ id: "settings", label: "设置", onSelect: vi.fn() }]} />);
    const trigger = screen.getByRole("button", { name: "更多" });
    vi.spyOn(trigger, "getBoundingClientRect").mockReturnValueOnce(new DOMRect(16, 500, 200, 44));
    await userEvent.click(trigger);
    const menu = screen.getByRole("menu", { name: "更多" });
    expect(menu.style.bottom).toBe(`${window.innerHeight - 500 + 4}px`);
    expect(menu.style.top).toBe("");
    expect(menu.style.maxHeight).toBe("492px");
    expect(menu.parentElement).toBe(document.body);
  });

  it("菜单项图标不进入可访问名称，带图标的项仍能执行", async () => {
    const select = vi.fn();
    render(<MenuButton label="更多" actions={[{ id: "settings", label: "设置", icon: <svg role="img" aria-label="设置图标"><title>齿轮</title></svg>, onSelect: select }]} />);
    await userEvent.click(screen.getByRole("button", { name: "更多" }));
    const item = screen.getByRole("menuitem", { name: "设置" });
    expect(screen.queryByRole("img", { name: "设置图标" })).toBeNull();
    expect(item.querySelector("svg")?.closest('[aria-hidden="true"]')).toBeTruthy();
    await userEvent.click(item);
    expect(select).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("showModal 对话框内的菜单 portal 到该对话框，键盘选择和 Esc 关闭不关闭外层对话框", async () => {
    const select = vi.fn();
    render(<dialog aria-label="菜单容器"><MenuButton label="更多" actions={[{ id: "settings", label: "设置", onSelect: select }]} /></dialog>);
    const dialog = screen.getByRole<HTMLDialogElement>("dialog", { hidden: true });
    dialog.showModal();
    expect(screen.getByRole("dialog", { name: "菜单容器" })).toBe(dialog);
    const trigger = screen.getByRole("button", { name: "更多" });
    trigger.focus();
    await userEvent.keyboard("{ArrowDown}");
    const menu = screen.getByRole("menu", { name: "更多" });
    expect(menu.parentElement).toBe(dialog);
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "设置" }));
    await userEvent.keyboard("{Escape}");
    expect(screen.queryByRole("menu")).toBeNull();
    expect(dialog.open).toBe(true);
    expect(document.activeElement).toBe(trigger);
    await userEvent.keyboard("{Enter}{Enter}");
    expect(select).toHaveBeenCalledTimes(1);
    expect(dialog.open).toBe(true);
    expect(document.activeElement).toBe(trigger);
  });
});
