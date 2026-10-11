import { vi } from "vitest";
import type { PlayedTrack, PlayRecorder } from "../features/player/engine/player-engine";

/** 什么也不做的记录出口：不关心计次的测试用它构造引擎。 */
export const silentPlayRecorder: PlayRecorder = { record: () => undefined, discard: () => undefined };

/** 记录出口的替身：record 收集引擎交出的播放，discard 记录被调用的次数。 */
export function fakePlayRecorder() {
  const played: PlayedTrack[] = [];
  const discard = vi.fn<() => void>();
  const recorder: PlayRecorder = { record: (play) => void played.push(play), discard };
  return { recorder, played, discard };
}
