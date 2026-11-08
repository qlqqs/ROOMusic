import { act, createEvent, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Button } from "./button";

function pointerEnter(element: HTMLElement, pointerType: string) {
  const event = createEvent.pointerOver(element);
  Object.defineProperty(event, "pointerType", { value: pointerType });
  fireEvent(element, event);
}

afterEach(() => {
  vi.useRealTimers();
});

describe("图标按钮悬停提示", () => {
  it("鼠标停留 300ms 后显示提示", () => {
    vi.useFakeTimers();
    render(<Button variant="icon" icon={<span>+</span>} label="新增目录" />);
    const button = screen.getByRole("button", { name: "新增目录" });

    pointerEnter(button, "mouse");
    act(() => vi.advanceTimersByTime(299));
    expect(screen.queryByText("新增目录")).toBeNull();
    act(() => vi.advanceTimersByTime(1));
    expect(screen.getByText("新增目录").getAttribute("aria-hidden")).toBe("true");
  });

  it("鼠标移开后立即隐藏提示", () => {
    vi.useFakeTimers();
    render(<Button variant="icon" icon={<span>+</span>} label="新增目录" />);
    const button = screen.getByRole("button", { name: "新增目录" });

    pointerEnter(button, "mouse");
    act(() => vi.advanceTimersByTime(300));
    expect(screen.getByText("新增目录")).toBeTruthy();
    const leave = createEvent.pointerOut(button);
    Object.defineProperty(leave, "pointerType", { value: "mouse" });
    fireEvent(button, leave);
    expect(screen.queryByText("新增目录")).toBeNull();
  });

  it("按下后隐藏并且指针未离开重入前不残留", () => {
    vi.useFakeTimers();
    render(<Button variant="icon" icon={<span>+</span>} label="新增目录" />);
    const button = screen.getByRole("button", { name: "新增目录" });

    pointerEnter(button, "mouse");
    act(() => vi.advanceTimersByTime(300));
    fireEvent.pointerDown(button);
    expect(screen.queryByText("新增目录")).toBeNull();
    act(() => vi.advanceTimersByTime(300));
    expect(screen.queryByText("新增目录")).toBeNull();
  });

  it("键盘聚焦和非鼠标指针不显示提示", () => {
    vi.useFakeTimers();
    render(<Button variant="icon" icon={<span>+</span>} label="新增目录" />);
    const button = screen.getByRole("button", { name: "新增目录" });

    button.focus();
    act(() => vi.advanceTimersByTime(300));
    expect(screen.queryByText("新增目录")).toBeNull();
    pointerEnter(button, "touch");
    act(() => vi.advanceTimersByTime(300));
    expect(screen.queryByText("新增目录")).toBeNull();
  });
});
