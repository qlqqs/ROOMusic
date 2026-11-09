// 浏览器输出端认领时声明的能力（合同 §2.6、§8；候选表见 dsp-pipeline design-delivery.md §3、§5.7）。
// 只用 canPlayType 探测：两项 HLS 候选都能探测到（只有 WebKit，含 iOS 上所有浏览器与桌面 Safari）时只声明 HLS 候选，
// 否则按长流候选表的顺序放进能解的。音量按“写 0.5 读回”判定，不参与 HLS 判定（iOS 27 的 Edge 写入能读回）。
import type { Capabilities } from "../zones/model";

type Candidate = Capabilities["stream_formats"][number];

const candidates: readonly Candidate[] = [
  { codec: "flac", mime_type: "audio/flac", lossless: true, max_sample_rate: 192_000, max_bit_depth: 24 },
  { codec: "wav", mime_type: "audio/wav", lossless: true, max_sample_rate: 192_000, max_bit_depth: 24 },
  { codec: "opus", mime_type: 'audio/ogg; codecs="opus"', lossless: false, max_sample_rate: null, max_bit_depth: null },
  { codec: "aac", mime_type: "audio/aac", lossless: false, max_sample_rate: null, max_bit_depth: null },
];

const hlsMimeType = "application/vnd.apple.mpegurl";

// HLS 候选与各自要探测的类型（播放列表类型与分片里的编码都要非空）。
const hlsCandidates: readonly { candidate: Candidate; probes: readonly string[] }[] = [
  {
    candidate: { codec: "alac", mime_type: hlsMimeType, lossless: true, max_sample_rate: 192_000, max_bit_depth: 24 },
    probes: [hlsMimeType, 'audio/mp4; codecs="alac"'],
  },
  {
    candidate: { codec: "aac", mime_type: hlsMimeType, lossless: false, max_sample_rate: 48_000, max_bit_depth: null },
    probes: [hlsMimeType, 'audio/mp4; codecs="mp4a.40.2"'],
  },
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
  const canPlay = (type: string) => element.canPlayType(type) !== "";
  const hls = hlsCandidates.every(({ probes }) => probes.every(canPlay));
  return {
    stream_formats: hls
      ? hlsCandidates.map(({ candidate }) => candidate)
      : candidates.filter((candidate) => canPlay(candidate.mime_type)),
    max_channels: 2,
    dsd: "none",
    exclusive: false,
    volume: volumeControllable(element) ? "number" : "fixed",
  };
}
