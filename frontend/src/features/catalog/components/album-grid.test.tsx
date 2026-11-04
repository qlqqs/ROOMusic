import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AlbumGridLayout, AlbumGridSkeleton } from "./album-grid";

describe("AlbumGridLayout", () => {
  it("通用网格保留调用方的 li 子项、顺序与交互内容", () => {
    render(
      <AlbumGridLayout>
        <li><a href="#first">第一张专辑</a></li>
        <li><button type="button">收藏第二张专辑</button></li>
      </AlbumGridLayout>,
    );
    const list = screen.getByRole("list");
    const items = within(list).getAllByRole("listitem");
    expect(list.tagName).toBe("UL");
    expect(items.map((item) => item.textContent)).toEqual(["第一张专辑", "收藏第二张专辑"]);
    expect(Array.from(list.children)).toEqual(items);
    expect(within(list).getByRole("link", { name: "第一张专辑" }).getAttribute("href")).toBe("#first");
    expect(within(list).getByRole("button", { name: "收藏第二张专辑" })).toBeTruthy();
  });
});

describe("AlbumGridSkeleton", () => {
  it.each([
    { label: "默认", count: undefined, expected: 12 },
    { label: "指定八张", count: 8, expected: 8 },
    { label: "指定零张", count: 0, expected: 0 },
  ])("$label：按数量渲染占位，只向读屏提供加载说明", ({ count, expected }) => {
    render(count === undefined ? <AlbumGridSkeleton /> : <AlbumGridSkeleton count={count} />);
    expect(screen.getByRole("status").textContent).toBe("正在加载专辑");
    const list = screen.getByRole("list", { hidden: true });
    expect(list.getAttribute("aria-hidden")).toBe("true");
    expect(within(list).queryAllByRole("listitem", { hidden: true })).toHaveLength(expected);
    expect(screen.queryByRole("list")).toBeNull();
    expect(screen.queryAllByRole("listitem")).toHaveLength(0);
  });
});
