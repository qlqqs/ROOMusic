import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { dsdRateName } from "../../../shared/lib/dsd";
import { loadRawApiSample } from "../../../test/api-samples";
import { fakeZoneServer, testPlayerController } from "../../../test/fake-zones";
import type { PlayerSnapshot } from "../zones/controller";
import { decodeStreamEvent } from "../zones/decoders";
import { SignalPathButton } from "./signal-path";

vi.mock("../player-provider", () => ({
  usePlayerSelector: function useTestPlayerSelector<T>(select: (snapshot: PlayerSnapshot) => T) {
    return select(snapshot);
  },
}));
vi.mock("../hooks/use-track-analysis", () => ({ useTrackAnalysis: () => null }));

let snapshot: PlayerSnapshot;

it("按 DSD 倍率显示采样率，并显示 DSF 编码及 DSD 到 PCM 的信号路径", async () => {
  expect(dsdRateName(2822400)).toBe("DSD64");
  expect(dsdRateName(5644800)).toBe("DSD128");
  expect(dsdRateName(3072000)).toBe("DSD64（48k）");
  expect(dsdRateName(352800)).toBeNull();

  const sample = loadRawApiSample("playback-zones/events/zone-signal-dsd");
  if (typeof sample !== "object" || sample === null || !("event" in sample) || !("data" in sample) || typeof sample.event !== "string") {
    throw new Error("DSD 样本不是 {event, data} 结构");
  }
  const event = decodeStreamEvent(sample.event, JSON.stringify(sample.data));
  if (event?.type !== "zone") throw new Error("DSD 样本不是播放区事件");
  expect(event.zone.signal?.steps.some((step) => step.kind === "dsd_to_pcm")).toBe(true);

  const server = fakeZoneServer();
  snapshot = {
    ...testPlayerController(server).controller.getSnapshot(),
    current: event.zone.current,
    zone: event.zone,
    device: server.device(),
  };
  const user = userEvent.setup();
  render(<SignalPathButton />);
  await user.click(screen.getByRole("button", { name: "信号路径：高质量" }));
  const dialog = screen.getByRole("dialog", { name: "信号路径：高质量" });
  expect(within(dialog).getByText("DSD（DSF） DSD64", { exact: true })).toBeTruthy();
  expect(within(dialog).getByText("DSD64 → PCM 352.8 kHz", { exact: true })).toBeTruthy();
});
