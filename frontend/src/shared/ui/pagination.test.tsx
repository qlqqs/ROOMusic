import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Pagination, type PaginationLinkProps } from "./pagination";

// 调用方负责生成链接：测试里用普通 <a>，组件本身不依赖路由。
const renderLink = (page: number, { className, rel, children }: PaginationLinkProps) => (
  <a href={`/list?page=${page}`} className={className} rel={rel}>
    {children}
  </a>
);

describe("Pagination", () => {
  it("只有一页或没有数据时不渲染", () => {
    const { container, rerender } = render(<Pagination label="记录分页" page={1} pageCount={1} renderLink={renderLink} />);
    expect(container.innerHTML).toBe("");
    rerender(<Pagination label="记录分页" page={1} pageCount={0} renderLink={renderLink} />);
    expect(container.innerHTML).toBe("");
  });

  it("中间页：nav 有可访问名称，上一页与下一页是链接，当前页文字带 aria-current", () => {
    render(<Pagination label="扫描记录分页" page={3} pageCount={5} renderLink={renderLink} />);
    const nav = screen.getByRole("navigation", { name: "扫描记录分页" });
    const previous = within(nav).getByRole("link", { name: "上一页" });
    const next = within(nav).getByRole("link", { name: "下一页" });
    expect(previous.getAttribute("href")).toBe("/list?page=2");
    expect(previous.getAttribute("rel")).toBe("prev");
    expect(next.getAttribute("href")).toBe("/list?page=4");
    expect(next.getAttribute("rel")).toBe("next");
    const current = within(nav).getByText("第 3 / 5 页");
    expect(current.getAttribute("aria-current")).toBe("page");
  });

  it("第一页：上一页是不可用的文字，不是链接；最后一页同理", () => {
    const { rerender } = render(<Pagination label="记录分页" page={1} pageCount={3} renderLink={renderLink} />);
    expect(screen.queryByRole("link", { name: "上一页" })).toBeNull();
    expect(screen.getByText("上一页").getAttribute("aria-disabled")).toBe("true");
    expect(screen.getByRole("link", { name: "下一页" }).getAttribute("href")).toBe("/list?page=2");

    rerender(<Pagination label="记录分页" page={3} pageCount={3} renderLink={renderLink} />);
    expect(screen.queryByRole("link", { name: "下一页" })).toBeNull();
    expect(screen.getByText("下一页").getAttribute("aria-disabled")).toBe("true");
    expect(screen.getByRole("link", { name: "上一页" }).getAttribute("href")).toBe("/list?page=2");
  });

  it("页码超出末页时，上一页指向末页而不是另一个越界的页", () => {
    render(<Pagination label="记录分页" page={9} pageCount={3} renderLink={renderLink} />);
    expect(screen.getByRole("link", { name: "上一页" }).getAttribute("href")).toBe("/list?page=3");
    expect(screen.queryByRole("link", { name: "下一页" })).toBeNull();
  });
});
