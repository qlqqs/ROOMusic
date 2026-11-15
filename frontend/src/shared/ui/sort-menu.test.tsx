import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { SortMenu, type SortOption } from "./sort-menu";

type Sort = "title" | "added";
const options: readonly SortOption<Sort>[] = [
  { value: "title", label: "按标题" },
  { value: "added", label: "最近新增" },
];

function Harness({ onChange }: { onChange: (value: Sort) => void }) {
  const [value, setValue] = useState<Sort>("title");
  return (
    <SortMenu
      options={options}
      value={value}
      onChange={(next) => {
        onChange(next);
        setValue(next);
      }}
    />
  );
}

describe("SortMenu", () => {
  it("按钮写着当前排序（名称“排序：按标题”）；菜单名“排序”，当前项带勾与“当前”", async () => {
    const user = userEvent.setup();
    render(<Harness onChange={vi.fn()} />);
    const trigger = screen.getByRole("button", { name: "排序：按标题" });
    expect(trigger.textContent).toContain("按标题");
    await user.click(trigger);
    expect(screen.getByRole("menu", { name: "排序" })).toBeTruthy();
    expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(["按标题 当前", "最近新增"]);
    // 每一项都占着图标位（当前项是勾），文字对齐。
    expect(screen.getByRole("menuitem", { name: "按标题 当前" }).querySelector("svg")).not.toBeNull();
    expect(screen.getByRole("menuitem", { name: "最近新增" }).hasAttribute("data-icon")).toBe(true);
  });

  it("选别的项：调用 onChange、菜单关闭、焦点回到按钮，按钮名称随之改变；再选当前项不调用", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    await user.click(screen.getByRole("button", { name: "排序：按标题" }));
    await user.click(screen.getByRole("menuitem", { name: "最近新增" }));
    expect(onChange).toHaveBeenCalledWith("added");
    expect(screen.queryByRole("menu")).toBeNull();
    const trigger = screen.getByRole("button", { name: "排序：最近新增" });
    expect(document.activeElement).toBe(trigger);
    await user.click(trigger);
    await user.click(screen.getByRole("menuitem", { name: "最近新增 当前" }));
    expect(onChange).toHaveBeenCalledTimes(1);
  });

  it("键盘：下箭头打开，回车选中", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(<Harness onChange={onChange} />);
    screen.getByRole("button", { name: "排序：按标题" }).focus();
    await user.keyboard("{ArrowDown}{ArrowDown}{Enter}");
    expect(onChange).toHaveBeenCalledWith("added");
  });
});
