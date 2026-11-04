import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
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

  it("有请求 ID 时显示，没有时不显示", () => {
    const { rerender } = render(<Notice requestId="web-0123456789abcdef">说明</Notice>);
    expect(screen.getByText("web-0123456789abcdef")).toBeTruthy();
    rerender(<Notice>说明</Notice>);
    expect(screen.queryByText(/请求 ID/)).toBeNull();
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
  it("把 http 失败转成中文文案并显示请求 ID，feature 文案优先", () => {
    const error = new ApiError({ kind: "http", status: 409, code: "last_admin", message: "x", requestId: "web-abc" });
    render(<FailureNotice error={error} messages={{ last_admin: "不能停用最后一个管理员。" }} />);
    const alert = screen.getByRole("alert");
    expect(alert.textContent).toContain("不能停用最后一个管理员。");
    expect(alert.textContent).toContain("web-abc");
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
