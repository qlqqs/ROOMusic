import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { TextField } from "./text-field";

describe("TextField", () => {
  it("label 与输入框关联，可按名称取得并输入", async () => {
    render(<TextField label="用户名" />);
    const input = screen.getByRole("textbox", { name: "用户名" });
    await userEvent.type(input, "curator");
    expect((input as HTMLInputElement).value).toBe("curator");
  });

  it("说明文字通过 aria-describedby 关联到输入框", () => {
    render(<TextField label="密码" type="password" description="至少 12 个字符（按 UTF-8 字节计算）" />);
    const input = screen.getByLabelText("密码");
    expect(input.getAttribute("aria-invalid")).toBeNull();
    expect(input.getAttribute("aria-describedby")).toBe(screen.getByText(/至少 12 个字符/).id);
  });

  it("字段错误同时标记 aria-invalid 并与输入框关联，说明与错误都可读", () => {
    render(<TextField label="用户名" description="区分大小写" error="用户名已存在" />);
    const input = screen.getByRole("textbox", { name: "用户名" });
    expect(input.getAttribute("aria-invalid")).toBe("true");
    const describedBy = (input.getAttribute("aria-describedby") ?? "").split(" ");
    expect(describedBy).toHaveLength(2);
    const texts = describedBy.map((id) => document.getElementById(id)?.textContent);
    expect(texts).toEqual(["区分大小写", "用户名已存在"]);
  });

  it("没有说明与错误时不写 aria-describedby", () => {
    render(<TextField label="用户名" />);
    expect(screen.getByRole("textbox", { name: "用户名" }).hasAttribute("aria-describedby")).toBe(false);
  });

  it("传递 ref 以便提交失败后把焦点移到第一个出错字段", () => {
    let element: HTMLInputElement | null = null;
    render(<TextField label="用户名" ref={(node) => { element = node; }} />);
    expect(element).toBe(screen.getByRole("textbox", { name: "用户名" }));
  });
});
