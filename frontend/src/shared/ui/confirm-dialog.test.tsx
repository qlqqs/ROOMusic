import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { ConfirmDialog } from "./confirm-dialog";

function setup(props: Partial<Parameters<typeof ConfirmDialog>[0]> = {}) {
  const onConfirm = vi.fn();
  const onCancel = vi.fn();
  render(
    <ConfirmDialog open title="停用目录" confirmLabel="停用" onConfirm={onConfirm} onCancel={onCancel} {...props}>
      扫描将跳过该目录。
    </ConfirmDialog>,
  );
  return { onConfirm, onCancel };
}

function pressEscape() {
  const dialog = screen.getByRole("dialog");
  const event = new Event("cancel", { cancelable: true });
  fireEvent(dialog, event);
  return event;
}

describe("ConfirmDialog", () => {
  it("关闭时不渲染内容", () => {
    setup({ open: false });
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByText("扫描将跳过该目录。")).toBeNull();
  });

  it("打开后对话框有名称，说明可见，初始焦点在“取消”上而不是确认按钮", () => {
    setup();
    expect(screen.getByRole("dialog", { name: "停用目录" })).toBeTruthy();
    expect(screen.getByText("扫描将跳过该目录。")).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("button", { name: "取消" }));
  });

  it("点击确认与取消分别发出对应意图", async () => {
    const { onConfirm, onCancel } = setup();
    await userEvent.click(screen.getByRole("button", { name: "停用" }));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    await userEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("Esc 触发取消，并阻止浏览器自行关闭", () => {
    const { onCancel } = setup();
    const event = pressEscape();
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(event.defaultPrevented).toBe(true);
  });

  it("提交中：确认按钮忙碌，取消与 Esc 暂时无效", async () => {
    const { onConfirm, onCancel } = setup({ pending: true });
    const confirm = screen.getByRole("button", { name: "停用" });
    expect(confirm.getAttribute("aria-busy")).toBe("true");
    await userEvent.click(confirm);
    expect(onConfirm).not.toHaveBeenCalled();
    pressEscape();
    expect(onCancel).not.toHaveBeenCalled();
    expect((screen.getByRole("button", { name: "取消" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("阻塞性错误显示在对话框内", () => {
    setup({ error: <p role="alert">目录已变化</p> });
    expect(screen.getByRole("dialog").textContent).toContain("目录已变化");
  });

  it("关闭后焦点回到打开前的触发按钮", async () => {
    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button type="button" onClick={() => setOpen(true)}>
            打开
          </button>
          <ConfirmDialog open={open} title="停用目录" confirmLabel="停用" onConfirm={() => undefined} onCancel={() => setOpen(false)}>
            说明
          </ConfirmDialog>
        </>
      );
    }
    render(<Harness />);
    const trigger = screen.getByRole("button", { name: "打开" });
    await userEvent.click(trigger);
    expect(screen.getByRole("dialog")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });
});
