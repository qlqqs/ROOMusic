import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { useClampedPage } from "./use-clamped-page";

function setup(options: { page: number; pageCount: number; ready: boolean }) {
  const onOutOfRange = vi.fn();
  const view = renderHook((props: typeof options) => useClampedPage({ ...props, onOutOfRange }), { initialProps: options });
  return { onOutOfRange, view };
}

describe("useClampedPage", () => {
  it("页码在范围内：不回调，也不算越界", () => {
    const { onOutOfRange, view } = setup({ page: 3, pageCount: 3, ready: true });
    expect(view.result.current).toBe(false);
    expect(onOutOfRange).not.toHaveBeenCalled();
  });

  it("页码超出末页：回调末页并报告越界", () => {
    const { onOutOfRange, view } = setup({ page: 9, pageCount: 3, ready: true });
    expect(view.result.current).toBe(true);
    expect(onOutOfRange).toHaveBeenCalledExactlyOnceWith(3);
  });

  it("没有数据而页码大于 1：回到第 1 页", () => {
    const { onOutOfRange, view } = setup({ page: 2, pageCount: 0, ready: true });
    expect(view.result.current).toBe(true);
    expect(onOutOfRange).toHaveBeenCalledExactlyOnceWith(1);
  });

  it("没有数据而在第 1 页：不越界（显示空状态）", () => {
    const { onOutOfRange, view } = setup({ page: 1, pageCount: 0, ready: true });
    expect(view.result.current).toBe(false);
    expect(onOutOfRange).not.toHaveBeenCalled();
  });

  it("数据还不能用来判断（加载中或占位数据）：不回调；就绪之后才判断", () => {
    const { onOutOfRange, view } = setup({ page: 9, pageCount: 3, ready: false });
    expect(view.result.current).toBe(false);
    expect(onOutOfRange).not.toHaveBeenCalled();
    view.rerender({ page: 9, pageCount: 3, ready: true });
    expect(view.result.current).toBe(true);
    expect(onOutOfRange).toHaveBeenCalledExactlyOnceWith(3);
  });

  it("页码被规范化之后不再重复回调", () => {
    const { onOutOfRange, view } = setup({ page: 9, pageCount: 3, ready: true });
    view.rerender({ page: 3, pageCount: 3, ready: true });
    expect(view.result.current).toBe(false);
    expect(onOutOfRange).toHaveBeenCalledTimes(1);
  });
});
