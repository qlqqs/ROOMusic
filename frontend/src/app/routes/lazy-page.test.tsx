import { lazyRouteComponent } from "@tanstack/react-router";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LazyPage } from "./lazy-page";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("LazyPage", () => {
  it("代码块下载期间显示加载占位，下载完成后显示页面", async () => {
    let resolve: (module: { Page: () => React.JSX.Element }) => void = () => undefined;
    const Page = lazyRouteComponent(() => new Promise<{ Page: () => React.JSX.Element }>((done) => { resolve = done; }), "Page");
    await act(async () => {
      render(<LazyPage><Page /></LazyPage>);
    });

    expect(screen.getByRole("status").textContent).toBe("正在加载页面");
    await act(async () => {
      resolve({ Page: () => <h1>成员</h1> });
    });
    expect(screen.getByRole("heading", { name: "成员" })).toBeDefined();
  });

  it("代码块加载失败时显示错误与刷新页面，而不是空白", async () => {
    // React 会把错误边界捕获的错误打到控制台；这里是预期的失败。
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    const reload = vi.fn();
    vi.spyOn(window, "location", "get").mockReturnValue({ ...window.location, reload });
    const Page = lazyRouteComponent(() => Promise.reject(new Error("network down")), "Page");
    await act(async () => {
      render(<LazyPage><Page /></LazyPage>);
    });

    expect(screen.getByRole("alert").textContent).toContain("页面没能加载");
    await userEvent.click(screen.getByRole("button", { name: "刷新页面" }));
    expect(reload).toHaveBeenCalledOnce();
  });
});
