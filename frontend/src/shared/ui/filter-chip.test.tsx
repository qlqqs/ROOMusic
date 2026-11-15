import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { FilterChip } from "./filter-chip";

describe("FilterChip", () => {
  it("是带 aria-pressed 的普通按钮，点击与空格都调用 onClick", async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    const { rerender } = render(<FilterChip pressed={false} onClick={onClick}>作曲</FilterChip>);
    const chip = screen.getByRole("button", { name: "作曲" });
    expect(chip.getAttribute("type")).toBe("button");
    expect(chip.getAttribute("aria-pressed")).toBe("false");
    await user.click(chip);
    chip.focus();
    await user.keyboard(" ");
    expect(onClick).toHaveBeenCalledTimes(2);
    rerender(<FilterChip pressed onClick={onClick}>作曲</FilterChip>);
    expect(screen.getByRole("button", { name: "作曲", pressed: true })).toBeTruthy();
  });
});
