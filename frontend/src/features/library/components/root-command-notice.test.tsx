import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { RootCommandState } from "../hooks/use-root-commands";
import { RootCommandNotice } from "./root-command-notice";

const target = { id: "00000000-0000-4000-8000-000000000001", name: "测试目录", revision: 1 };
describe("目录停用与恢复的播放说明", () => {
  it.each(["disable", "restore"] as const)("%s 成功说明播放影响", (kind) => {
    const state: RootCommandState = { status: "succeeded", command: { kind, root: target, key: "test-key" }, result: { kind, name: target.name, root: { id: target.id, path: "safe-test-root", status: kind === "disable" ? "disabled" : "active", revision: 2 } } };
    render(<RootCommandNotice state={state} onRetry={vi.fn()} onDismiss={vi.fn()} />);
    const notice = screen.getByRole("status");
    if (kind === "disable") {
      expect(notice.textContent).toContain("里面的曲目暂时不能播放");
      expect(notice.textContent).toContain("恢复后就能再播");
    } else expect(notice.textContent).toContain("里面的曲目可以播放了");
    expect(notice.textContent).not.toContain("safe-test-root");
  });
});
