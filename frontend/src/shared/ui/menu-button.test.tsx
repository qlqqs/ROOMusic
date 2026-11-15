import { render, screen, within } from "@testing-library/react";
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

describe("菜单分组", () => {
  const actions = [
    { id: "next", label: "下一首播放", onSelect: vi.fn() },
    { id: "enqueue", label: "加入队列", onSelect: vi.fn() },
    { id: "edit", group: "管理", label: "编辑发行信息", onSelect: vi.fn() },
    { id: "join", group: "管理", label: "归入其他专辑…", onSelect: vi.fn() },
  ];

  it("组与组之间一条分隔线，有名字的组带组标题，组的名称就是标题；无名组的项直接在菜单里", async () => {
    const user = userEvent.setup();
    render(<MenuButton label="更多" actions={actions} />);
    await user.click(screen.getByRole("button", { name: "更多" }));
    const menu = screen.getByRole("menu", { name: "更多" });
    expect(within(menu).getAllByRole("separator")).toHaveLength(1);
    const group = within(menu).getByRole("group", { name: "管理" });
    expect(within(group).getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["编辑发行信息", "归入其他专辑…"]);
    // 无名组的项是菜单的直接子元素，与没有分组时的结构相同。
    expect([...menu.children].filter((child) => child.getAttribute("role") === "menuitem").map((item) => item.textContent)).toEqual(["下一首播放", "加入队列"]);
    // 分隔线在两组之间。
    const separator = within(menu).getByRole("separator");
    expect(separator.previousElementSibling?.textContent).toBe("加入队列");
    expect(separator.nextElementSibling).toBe(group);
  });

  it("组标题不可聚焦，方向键跨组移动时跳过它；Home、End 到两端的菜单项", async () => {
    const user = userEvent.setup();
    render(<MenuButton label="更多" actions={actions} />);
    const trigger = screen.getByRole("button", { name: "更多" });
    trigger.focus();
    await user.keyboard("{ArrowDown}");
    const title = screen.getByText("管理", { exact: true });
    expect(title.tabIndex).toBe(-1);
    expect(title.hasAttribute("tabindex")).toBe(false);
    expect(title.tagName).not.toBe("BUTTON");
    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "加入队列" }));
    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "编辑发行信息" }));
    await user.keyboard("{ArrowUp}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "加入队列" }));
    await user.keyboard("{End}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "归入其他专辑…" }));
    await user.keyboard("{ArrowDown}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "下一首播放" }));
    await user.keyboard("{ArrowUp}{Enter}");
    expect(actions[3]?.onSelect).toHaveBeenCalledTimes(1);
  });

  it("所有项都没有组：没有分隔线与组", async () => {
    const user = userEvent.setup();
    render(<MenuButton label="更多" actions={actions.slice(0, 2)} />);
    await user.click(screen.getByRole("button", { name: "更多" }));
    expect(screen.queryByRole("separator")).toBeNull();
    expect(screen.queryByRole("group")).toBeNull();
  });

  it("不相邻的同名组各成一段", async () => {
    const user = userEvent.setup();
    render(<MenuButton label="更多" actions={[{ ...actions[2], id: "a" }, { id: "b", label: "加入队列", onSelect: vi.fn() }, { ...actions[3], id: "c" }] as typeof actions} />);
    await user.click(screen.getByRole("button", { name: "更多" }));
    expect(screen.getAllByRole("group", { name: "管理" })).toHaveLength(2);
    expect(screen.getAllByRole("separator")).toHaveLength(2);
  });

  it("untitledGroup：这一组只用分隔线分开，没有组标题也不包 role=group；方向键照常经过", async () => {
    const user = userEvent.setup();
    const go = vi.fn();
    render(<MenuButton label="更多" actions={[...actions.slice(0, 2), { id: "artist", group: "go", untitledGroup: true, label: "前往艺人", onSelect: go }, { id: "album", group: "go", untitledGroup: true, label: "前往专辑", onSelect: vi.fn() }, ...actions.slice(2)]} />);
    const trigger = screen.getByRole("button", { name: "更多" });
    trigger.focus();
    await user.keyboard("{ArrowDown}");
    const menu = screen.getByRole("menu", { name: "更多" });
    expect(within(menu).getAllByRole("separator")).toHaveLength(2);
    // 只有“管理”一个带标题的组。
    expect(within(menu).getAllByRole("group")).toHaveLength(1);
    expect(within(menu).getByRole("group", { name: "管理" })).toBeTruthy();
    expect(screen.queryByText("go")).toBeNull();
    expect([...menu.children].filter((child) => child.getAttribute("role") === "menuitem").map((item) => item.textContent)).toEqual(["下一首播放", "加入队列", "前往艺人", "前往专辑"]);
    await user.keyboard("{ArrowDown}{ArrowDown}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "前往艺人" }));
    await user.keyboard("{Enter}");
    expect(go).toHaveBeenCalledTimes(1);
  });

  it("hint：项右侧的灰色小字是名称的一部分，与名称之间有空格", async () => {
    const user = userEvent.setup();
    render(<MenuButton label="更多" actions={[{ id: "artist", label: "前往艺人", hint: "邓丽君", onSelect: vi.fn() }, { id: "plain", label: "加入队列", onSelect: vi.fn() }]} />);
    await user.click(screen.getByRole("button", { name: "更多" }));
    const item = screen.getByRole("menuitem", { name: "前往艺人 邓丽君" });
    expect(item.hasAttribute("data-hint")).toBe(true);
    expect(screen.getByRole("menuitem", { name: "加入队列" }).hasAttribute("data-hint")).toBe(false);
  });
});

describe("破坏性的菜单项", () => {
  it("danger 的项另起一段（前面一条分隔线），带标记；同名组里的另一段不重复组标题", async () => {
    const user = userEvent.setup();
    render(
      <MenuButton
        label="更多"
        actions={[
          { id: "reset", label: "重置密码…", onSelect: vi.fn() },
          { id: "revoke", label: "撤销会话…", onSelect: vi.fn() },
          { id: "disable", label: "停用…", danger: true, onSelect: vi.fn() },
        ]}
      />,
    );
    await user.click(screen.getByRole("button", { name: "更多" }));
    const menu = screen.getByRole("menu", { name: "更多" });
    const separator = within(menu).getByRole("separator");
    expect(separator.previousElementSibling?.textContent).toBe("撤销会话…");
    expect(separator.nextElementSibling?.textContent).toBe("停用…");
    expect(screen.getByRole("menuitem", { name: "停用…" }).hasAttribute("data-danger")).toBe(true);
    expect(screen.getByRole("menuitem", { name: "撤销会话…" }).hasAttribute("data-danger")).toBe(false);
  });

  it("有名字的组里的破坏性项：分隔线隔开，组标题只出现一次", async () => {
    const user = userEvent.setup();
    render(
      <MenuButton
        label="更多"
        actions={[
          { id: "play", label: "播放", onSelect: vi.fn() },
          { id: "edit", group: "管理", label: "修改主名…", onSelect: vi.fn() },
          { id: "delete", group: "管理", label: "删除…", danger: true, onSelect: vi.fn() },
        ]}
      />,
    );
    await user.click(screen.getByRole("button", { name: "更多" }));
    const menu = screen.getByRole("menu", { name: "更多" });
    expect(within(menu).getAllByRole("separator")).toHaveLength(2);
    expect(within(menu).getAllByRole("group", { name: "管理" })).toHaveLength(1);
    expect(within(menu).getAllByText("管理", { exact: true })).toHaveLength(1);
    // 方向键照常走到破坏性项。
    await user.keyboard("{End}");
    expect(document.activeElement).toBe(screen.getByRole("menuitem", { name: "删除…" }));
  });

  it("相邻的多个破坏性项在同一段", async () => {
    const user = userEvent.setup();
    render(
      <MenuButton
        label="更多"
        actions={[
          { id: "download", label: "下载", onSelect: vi.fn() },
          { id: "a", label: "删除…", danger: true, onSelect: vi.fn() },
          { id: "b", label: "停用…", danger: true, onSelect: vi.fn() },
        ]}
      />,
    );
    await user.click(screen.getByRole("button", { name: "更多" }));
    expect(screen.getAllByRole("separator")).toHaveLength(1);
  });
});
