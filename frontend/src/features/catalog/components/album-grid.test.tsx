import { render, screen, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi } from "vitest";
import { albumGroups } from "../../../test/catalog-data";
import { AlbumGrid, AlbumGridLayout, AlbumGridSkeleton } from "./album-grid";

// 卡片是路由链接；这里只验证结构，不需要真实路由。
vi.mock("@tanstack/react-router", () => ({
  Link: ({ children, "aria-label": label, className }: { children: ReactNode; "aria-label"?: string; className?: string }) => <a href="/albums/x" aria-label={label} className={className}>{children}</a>,
}));

describe("AlbumGrid 的播放键", () => {
  it("不给 renderPlay：卡片里只有链接，没有按钮", () => {
    render(<AlbumGrid groups={albumGroups(2)} search={{}} />);
    expect(screen.getAllByRole("link")).toHaveLength(2);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("给了 renderPlay：每张卡片一个播放键，放在链接之外（链接里不嵌按钮），调用方拿到的是这张卡片的组", () => {
    const groups = albumGroups(2);
    render(<AlbumGrid groups={groups} search={{}} renderPlay={(group) => <button type="button">{`播放《${group.representative.title}》`}</button>} />);
    const items = screen.getAllByRole("listitem");
    expect(items).toHaveLength(2);
    items.forEach((item, index) => {
      const button = within(item).getByRole("button", { name: `播放《${groups[index]?.representative.title ?? ""}》` });
      expect(button.closest("a")).toBeNull();
      expect(within(within(item).getByRole("link")).queryByRole("button")).toBeNull();
    });
  });
});

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
