import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api";
import { FailureNotice } from "./failure-notice";
import { Notice } from "./notice";

describe("Notice", () => {
  it("错误用 role=alert，其余状态用 role=status", () => {
    const { rerender } = render(<Notice tone="error">失败</Notice>);
    expect(screen.getByRole("alert").textContent).toContain("失败");
    rerender(<Notice tone="success">已保存</Notice>);
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("status").textContent).toContain("已保存");
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  it("有错误编号时提供复制按钮，复制成功的读屏名保留两秒", async () => {
    userEvent.setup();
    vi.stubGlobal("isSecureContext", true);
    vi.useFakeTimers();
    const writeText = vi.spyOn(navigator.clipboard, "writeText").mockResolvedValue(undefined);
    const info = vi.spyOn(console, "info").mockImplementation(() => undefined);
    const requestId = "web-0123456789abcdef";
    render(<Notice requestId={requestId}>说明</Notice>);
    expect(screen.queryByText(requestId)).toBeNull();
    expect(info).toHaveBeenCalledExactlyOnceWith(`请求 ID：${requestId}`);
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "复制错误编号" }));
    });
    expect(writeText).toHaveBeenCalledExactlyOnceWith(requestId);
    expect(screen.getByRole("button", { name: "已复制错误编号" })).toBeTruthy();
    act(() => vi.advanceTimersByTime(1999));
    expect(screen.getByRole("button", { name: "已复制错误编号" })).toBeTruthy();
    act(() => vi.advanceTimersByTime(1));
    expect(screen.getByRole("button", { name: "复制错误编号" })).toBeTruthy();
  });

  it("没有剪贴板时点击按钮显示并选中错误编号", async () => {
    const user = userEvent.setup();
    vi.stubGlobal("isSecureContext", true);
    const clipboardHost: { readonly clipboard: Clipboard | undefined } = navigator;
    vi.spyOn(clipboardHost, "clipboard", "get").mockReturnValue(undefined);
    vi.spyOn(console, "info").mockImplementation(() => undefined);
    render(<Notice requestId="web-abc">说明</Notice>);
    await user.click(screen.getByRole("button", { name: "复制错误编号" }));
    expect(screen.getByText("web-abc")).toBeTruthy();
    expect(window.getSelection()?.toString()).toBe("web-abc");
  });

  it("没有错误编号时不提供复制按钮", () => {
    render(<Notice>说明</Notice>);
    expect(screen.queryByRole("button", { name: "复制错误编号" })).toBeNull();
  });

  it("显示标题与下一步操作", () => {
    render(
      <Notice tone="warning" title="目录已变化" action={<button type="button">重试</button>}>
        请刷新后再操作
      </Notice>,
    );
    expect(screen.getByText("目录已变化")).toBeTruthy();
    expect(screen.getByRole("button", { name: "重试" })).toBeTruthy();
  });
});

describe("FailureNotice", () => {
  it("把 http 失败转成中文文案，feature 文案优先，并提供错误编号复制按钮", () => {
    const error = new ApiError({ kind: "http", status: 409, code: "last_admin", message: "x", requestId: "web-abc" });
    render(<FailureNotice error={error} messages={{ last_admin: "不能停用最后一个管理员。" }} />);
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("不能停用最后一个管理员。");
    expect(alert.textContent).not.toContain("web-abc");
    expect(screen.getByRole("button", { name: "复制错误编号" })).toBeTruthy();
  });

  it("写请求的网络失败说明结果未知", () => {
    render(<FailureNotice error={new ApiError({ kind: "network", requestId: "web-1" })} context="write" />);
    expect(screen.getByRole("alert").textContent).toContain("不确定操作有没有完成");
  });

  it("不是 ApiError 的错误显示未预期的错误，不泄露内部信息", () => {
    render(<FailureNotice error={new Error("secret stack detail")} />);
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("未预期的错误");
    expect(alert.textContent).not.toContain("secret");
  });
});
