import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { EmptyState } from "./empty-state";

describe("EmptyState", () => {
  it("标题、说明与入口都在，图标对读屏隐藏", () => {
    const { container } = render(
      <EmptyState icon={<svg data-testid="icon" />} title="还没有歌单" action={<button type="button">新建歌单</button>}>
        把喜欢的曲目收成一组。
      </EmptyState>,
    );
    expect(screen.getByText("还没有歌单")).toBeTruthy();
    expect(screen.getByText("把喜欢的曲目收成一组。")).toBeTruthy();
    expect(screen.getByRole("button", { name: "新建歌单" })).toBeTruthy();
    expect(screen.getByTestId("icon").parentElement?.getAttribute("aria-hidden")).toBe("true");
    expect(container.textContent).toBe("还没有歌单把喜欢的曲目收成一组。新建歌单");
  });

  it("没有入口时不渲染入口的位置", () => {
    const { container } = render(<EmptyState icon={<svg />} title="空">说明</EmptyState>);
    expect(screen.queryByRole("button")).toBeNull();
    expect(container.firstElementChild?.children).toHaveLength(3);
  });
});
