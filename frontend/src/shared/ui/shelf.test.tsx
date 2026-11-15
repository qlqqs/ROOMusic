import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useShelf } from "./shelf";

function Harness() {
  const { ref, className, controls } = useShelf("测试");
  return (
    <section>
      <div data-testid="controls">{controls}</div>
      <ul ref={ref} className={className} data-testid="shelf">
        <li>一</li>
        <li>二</li>
      </ul>
    </section>
  );
}

/** jsdom 没有布局：给横排元素指定尺寸与滚动位置，再发一次 scroll 让 hook 重新量。 */
function layout(element: HTMLElement, { scrollWidth, clientWidth, scrollLeft }: { scrollWidth: number; clientWidth: number; scrollLeft: number }) {
  Object.defineProperty(element, "scrollWidth", { configurable: true, value: scrollWidth });
  Object.defineProperty(element, "clientWidth", { configurable: true, value: clientWidth });
  Object.defineProperty(element, "scrollLeft", { configurable: true, value: scrollLeft });
  act(() => {
    fireEvent.scroll(element);
  });
}

const previous = () => screen.queryByRole("button", { name: "测试：向前" });
const next = () => screen.queryByRole("button", { name: "测试：向后" });

afterEach(() => {
  vi.restoreAllMocks();
});

describe("useShelf", () => {
  it("内容放得下（不需要翻页）时没有翻页按钮", () => {
    render(<Harness />);
    expect(previous()).toBeNull();
    expect(next()).toBeNull();
    layout(screen.getByTestId("shelf"), { scrollWidth: 400, clientWidth: 400, scrollLeft: 0 });
    expect(previous()).toBeNull();
  });

  it("在开头：“向前”变淡但不禁用、按下不滚动，焦点留在按钮上；“向后”翻过约一屏", async () => {
    render(<Harness />);
    const shelf = screen.getByTestId("shelf");
    const scrollBy = vi.fn();
    shelf.scrollBy = scrollBy;
    layout(shelf, { scrollWidth: 1000, clientWidth: 400, scrollLeft: 0 });
    const back = previous();
    const forward = next();
    if (back === null || forward === null) throw new Error("没有翻页按钮");
    expect(back.hasAttribute("data-edge")).toBe(true);
    expect((back as HTMLButtonElement).disabled).toBe(false);
    expect(forward.hasAttribute("data-edge")).toBe(false);

    await userEvent.click(back);
    expect(scrollBy).not.toHaveBeenCalled();
    expect(document.activeElement).toBe(back);

    await userEvent.click(forward);
    expect(scrollBy).toHaveBeenCalledWith({ left: 360, behavior: "smooth" });
  });

  it("滚到末尾（差不到 8px 也算到头）：“向后”变淡、按下不滚动，“向前”往回翻", async () => {
    render(<Harness />);
    const shelf = screen.getByTestId("shelf");
    const scrollBy = vi.fn();
    shelf.scrollBy = scrollBy;
    layout(shelf, { scrollWidth: 1000, clientWidth: 400, scrollLeft: 594 });
    expect(next()?.hasAttribute("data-edge")).toBe(true);
    expect(previous()?.hasAttribute("data-edge")).toBe(false);
    await userEvent.click(next() as HTMLElement);
    expect(scrollBy).not.toHaveBeenCalled();
    await userEvent.click(previous() as HTMLElement);
    expect(scrollBy).toHaveBeenCalledWith({ left: -360, behavior: "smooth" });

    layout(shelf, { scrollWidth: 1000, clientWidth: 400, scrollLeft: 300 });
    expect(previous()?.hasAttribute("data-edge")).toBe(false);
    expect(next()?.hasAttribute("data-edge")).toBe(false);
  });

  it("减少动态效果时直接跳到位置，不做平滑滚动", async () => {
    vi.spyOn(window, "matchMedia").mockImplementation((query: string) => ({
      matches: query === "(prefers-reduced-motion: reduce)",
      media: query,
      onchange: null,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      addListener: () => undefined,
      removeListener: () => undefined,
      dispatchEvent: () => false,
    }));
    render(<Harness />);
    const shelf = screen.getByTestId("shelf");
    const scrollBy = vi.fn();
    shelf.scrollBy = scrollBy;
    layout(shelf, { scrollWidth: 1000, clientWidth: 400, scrollLeft: 0 });
    await userEvent.click(next() as HTMLElement);
    expect(scrollBy).toHaveBeenCalledWith({ left: 360, behavior: "auto" });
  });
});
