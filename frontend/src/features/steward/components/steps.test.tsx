import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { StepItem } from "../run/reduce";
import { Steps } from "./steps";

function item(callId: string, name: string, overrides: Partial<StepItem> = {}): StepItem {
  return { callId, name, input: {}, status: "succeeded", output: {}, errorCode: null, durationMs: 1_000, executed: true, ...overrides };
}

describe("过程摘要", () => {
  it("只有只读工具：查了 N 处资料", () => {
    render(<Steps items={[item("c1", "list_albums"), item("c2", "get_playback")]} live={false} />);
    expect(screen.getByText("查了 2 处资料，用时 2 秒")).toBeTruthy();
  });

  it("执行过改动类工具：做了 N 步", () => {
    render(<Steps items={[item("c1", "get_playback"), item("c2", "control_playback", { durationMs: null })]} live={false} />);
    expect(screen.getByText("做了 2 步，用时 1 秒")).toBeTruthy();
  });

  it("改动类工具没有执行（token_limit）时仍是查了 N 处资料", () => {
    render(<Steps items={[item("c1", "list_playlists"), item("c2", "create_playlist", { status: "failed", errorCode: "token_limit", durationMs: null, executed: false })]} live={false} />);
    expect(screen.getByText("查了 1 处资料，用时 1 秒")).toBeTruthy();
  });
});
