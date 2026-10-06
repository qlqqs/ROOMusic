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
