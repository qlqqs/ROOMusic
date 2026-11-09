// 浏览器输出端认领时声明的能力（合同 §2.6、§8；候选表见 dsp-pipeline design-delivery.md §3）。
// 按表的顺序用 canPlayType 探测，能解的按顺序放进 stream_formats；音量按“写 0.5 读回”判定（iPhone / iPad 的网页音量只读）。
import type { Capabilities } from "../zones/model";

type Candidate = Capabilities["stream_formats"][number];

const candidates: readonly Candidate[] = [
  { codec: "flac", mime_type: "audio/flac", lossless: true, max_sample_rate: 192_000, max_bit_depth: 24 },
  { codec: "wav", mime_type: "audio/wav", lossless: true, max_sample_rate: 192_000, max_bit_depth: 24 },
  { codec: "opus", mime_type: 'audio/ogg; codecs="opus"', lossless: false, max_sample_rate: null, max_bit_depth: null },
  { codec: "aac", mime_type: "audio/aac", lossless: false, max_sample_rate: null, max_bit_depth: null },
];

/** 元素能否由软件调音量：写 0.5 读回不变就是只读。 */
export function volumeControllable(element: { volume: number }): boolean {
  const before = element.volume;
  element.volume = 0.5;
  const controllable = element.volume === 0.5;
  element.volume = before;
  return controllable;
}

export function browserCapabilities(element: { canPlayType: (type: string) => string; volume: number }): Capabilities {
  return {
    stream_formats: candidates.filter((candidate) => element.canPlayType(candidate.mime_type) !== ""),
    max_channels: 2,
    dsd: "none",
    exclusive: false,
    volume: volumeControllable(element) ? "number" : "fixed",
  };
}
