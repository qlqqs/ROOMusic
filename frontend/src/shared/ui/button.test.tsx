import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Button } from "./button";

describe("Button", () => {
  it("默认 type 为 button，避免在表单里意外提交", () => {
    render(<Button>停用</Button>);
    expect(screen.getByRole("button", { name: "停用" }).getAttribute("type")).toBe("button");
  });

  it("点击时触发 onClick", async () => {
    const onClick = vi.fn();
    render(<Button onClick={onClick}>停用</Button>);
    await userEvent.click(screen.getByRole("button", { name: "停用" }));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("busy 时忽略点击，保留焦点能力，并通过 aria-busy 告知读屏", async () => {
    const onClick = vi.fn();
    render(
      <Button busy onClick={onClick}>
        正在提交
      </Button>,
    );
    const button = screen.getByRole("button", { name: "正在提交" });
    expect(button.getAttribute("aria-busy")).toBe("true");
    expect(button.getAttribute("aria-disabled")).toBe("true");
    expect((button as HTMLButtonElement).disabled).toBe(false);
    await userEvent.click(button);
    expect(onClick).not.toHaveBeenCalled();
  });

  it("disabled 的按钮不可点击", async () => {
    const onClick = vi.fn();
    render(
      <Button disabled onClick={onClick}>
        恢复
      </Button>,
    );
    await userEvent.click(screen.getByRole("button", { name: "恢复" }));
    expect(onClick).not.toHaveBeenCalled();
  });

  it("图标只是装饰，不进入可访问名称", () => {
    render(<Button icon={<svg data-testid="icon" />}>新增目录</Button>);
    expect(screen.getByRole("button", { name: "新增目录" })).toBeTruthy();
    expect(screen.getByTestId("icon").closest("[aria-hidden='true']")).not.toBeNull();
  });
});
