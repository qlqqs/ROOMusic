import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Skeleton } from "./skeleton";
import { StatusTag } from "./status-tag";

describe("StatusTag", () => {
  it("状态由文字表达，图标只是装饰", () => {
    render(<StatusTag icon={<svg data-testid="icon" />}>已停用</StatusTag>);
    expect(screen.getByText("已停用")).toBeTruthy();
    expect(screen.getByTestId("icon").closest("[aria-hidden='true']")).not.toBeNull();
  });
});

describe("Skeleton", () => {
  it("给读屏一个加载说明，占位块对读屏隐藏", () => {
    const { container } = render(<Skeleton label="正在加载目录" lines={2} />);
    expect(screen.getByRole("status").textContent).toBe("正在加载目录");
    expect(container.querySelectorAll("[aria-hidden='true']")).toHaveLength(2);
  });
});
