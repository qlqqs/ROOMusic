// 播放区协议 v1 在浏览器里的形状（合同 §2）：解码器把服务端的 JSON 换成这里的类型，界面与输出端只认这些类型。
// 时间都换成本地可比较的毫秒数（服务端时钟，用时钟偏移换算）；队列曲目换成 PlayerTrack。
import type { PeqDocument } from "../peq/document";
import type { PlayMode, PlayerTrack, QueueEntry } from "../engine/queue";

export const levelingModes = ["off", "auto", "track", "album"] as const;
export type LevelingMode = (typeof levelingModes)[number];

export const crossfadeDurations = [3, 5, 8, 12] as const;
export type CrossfadeSeconds = (typeof crossfadeDurations)[number];

export const streamQualities = ["lossless", "lossy"] as const;
export type StreamQuality = (typeof streamQualities)[number];

/** 采样率转换策略；服务端的保留值 dsd 不在界面出现，按 none 处理。 */
export const sampleRateStrategies = ["none", "max_sample_rate", "power_of_2", "custom"] as const;
export type SampleRateStrategy = (typeof sampleRateStrategies)[number];

export const sampleRateFilters = ["linear_precise", "minimum_precise", "linear_smooth", "minimum_smooth"] as const;
export type SampleRateFilter = (typeof sampleRateFilters)[number];

/** 降混步骤的做法：矩阵混合，或只取前几个声道。 */
export const downmixModes = ["matrix", "select"] as const;
export type DownmixMode = (typeof downmixModes)[number];

/** 指定采样率可选的标准率：44.1 与 48 kHz 两族的 1、2、4、8、16 倍。 */
export const customSampleRates = [44100, 48000, 88200, 96000, 176400, 192000, 352800, 384000, 705600, 768000] as const;

export type SampleRateSettings = { strategy: SampleRateStrategy; customRate: number | null; filter: SampleRateFilter };

/** 播放区的播放设置（服务端持有，作用于渲染流）。 */
export type PlaybackSettings = {
  leveling: LevelingMode;
  gapless: boolean;
  crossfade: boolean;
  crossfadeSeconds: CrossfadeSeconds;
  quality: StreamQuality;
  headroom: { enabled: boolean; db: number; clipIndicator: boolean };
  sampleRate: SampleRateSettings;
  downmix: { enabled: boolean };
  peq: PeqDocument;
  convolution: ConvolutionSettings;
};

/** 卷积校正：选一个本人的 IR；beforePeq 时卷积排在参数均衡之前。 */
export type ConvolutionSettings = { enabled: boolean; irId: string | null; beforePeq: boolean };

/** 曲目不能播放的原因；界面按它选文案。服务端以后增加的原因按“音频文件无法读取”显示。 */
export const playbackFailureReasons = ["not_found", "root_disabled", "unsupported_format", "source_unavailable", "source_changed", "decode_failed"] as const;
export type PlaybackFailureReason = (typeof playbackFailureReasons)[number];

/** 输出端回报的播放状态；服务端开新流时先置为 loading。 */
export type PlayerStatus = "idle" | "loading" | "playing" | "paused" | "buffering";

/** 停在暂停的几种故障：渲染器暂时不可用（可重试）、浏览器拒绝自动播放、这台设备放不了服务器给的格式。 */
export type PlayerFault = { kind: "transient" } | { kind: "autoplay_blocked" } | { kind: "no_stream_format" };

/** 被服务端拒绝、用户能自己处理的命令（目前只有目标设备已离线）。seq 每次拒绝递增，界面据此重新公告。 */
export type CommandRejection = { seq: number; reason: "zone_offline" };

/** 值得向读屏公告的播放区事件。seq 取产生事件时的 revision，界面据此只公告一次。 */
export type PlayerEvent =
  | { seq: number; kind: "track_started"; entryId: string }
  | { seq: number; kind: "track_failed"; entryId: string; reason: PlaybackFailureReason }
  | { seq: number; kind: "all_failed" }
  | { seq: number; kind: "failures_stopped" }
  | { seq: number; kind: "transient_failure" }
  | { seq: number; kind: "autoplay_blocked" }
  | { seq: number; kind: "no_stream_format" }
  | { seq: number; kind: "queue_restored"; restored: number; dropped: number }
  | { seq: number; kind: "play_recorded"; trackId: string };

/** 渲染流里的一段曲目：从流内 streamStartSeconds 起是这首的 trackStartSeconds 秒。 */
export type StreamSegment = { entryId: string; track: PlayerTrack; streamStartSeconds: number; trackStartSeconds: number };

export type ZoneStream = {
  seq: number;
  url: string;
  mimeType: string;
  quality: StreamQuality;
  /** 渲染会话确定首曲后给出；起始条目打不开的空流是 null（还不知道格式）。 */
  format: { codec: string; sampleRate: number; bitDepth: number | null; channels: number } | null;
  segments: readonly StreamSegment[];
};

/** 信号质量，从好到差：逐位、用户开的处理、为兼容做的转换、有损。 */
export const signalQualities = ["lossless", "enhanced", "high", "low"] as const;
export type SignalQuality = (typeof signalQualities)[number];

/** 信号路径的一步；params 按 kind 区分。 */
export type SignalStep = { quality: SignalQuality; lossy: boolean; compat: boolean } & (
  | { kind: "source"; params: { codec: string; sampleRate: number; bitDepth: number | null; channels: number } }
  | { kind: "convert"; params: { fromRate: number; toRate: number; by: string } }
  | { kind: "sample_rate"; params: { fromRate: number; toRate: number; filter: SampleRateFilter } }
  | { kind: "peq"; params: { label: string; bands: number } }
  | { kind: "convolution"; params: { name: string; taps: number; resampledFrom: number | null } }
  | { kind: "downmix"; params: { fromChannels: number; toChannels: number; fromLayout: string | null; toLayout: string | null; by: string; mode: DownmixMode | null } }
  | { kind: "leveling"; params: { gainDb: number; basis: string } }
  | { kind: "headroom"; params: { gainDb: number } }
  | { kind: "dither"; params: { bits: number; kind: string } }
  | { kind: "encode"; params: { codec: string; sampleRate: number; bitDepth: number | null; bitrateKbps: number | null } }
);

/** 信号路径：整体质量是各步里最差的一档；削波只在削波指示开着时有，处理速度是倍实时。 */
export type Signal = { quality: SignalQuality; steps: readonly SignalStep[]; clipping: boolean | null; processingSpeed: number | null };

export type Zone = {
  zoneId: string;
  deviceId: string;
  revision: number;
  queueRevision: number;
  queueLength: number;
  mode: PlayMode;
  settings: PlaybackSettings;
  current: QueueEntry | null;
  hasNext: boolean;
  intent: "play" | "pause";
  status: PlayerStatus;
  fault: PlayerFault | null;
  failures: Readonly<Record<string, PlaybackFailureReason>>;
  /** 曲目内位置；at 是服务端时钟的毫秒数。 */
  position: { seconds: number; at: number; durationSeconds: number | null; seekable: boolean };
  stream: ZoneStream | null;
  nextStream: ZoneStream | null;
  /** 意图变为暂停的时刻（服务端时钟毫秒）；播放中为 null。 */
  pausedAt: number | null;
  /** 正在放的那首的信号路径（dsp-pipeline 设计 §9.1）；没有在渲染时是 null。 */
  signal: Signal | null;
  event: PlayerEvent | null;
};

export type Device = {
  deviceId: string;
  zoneId: string;
  name: string;
  kind: string;
  online: boolean;
  lastSeenAt: number;
  volume: number;
  muted: boolean;
  /** 能由软件调音量（能力 volume 不是 fixed）。 */
  volumeControllable: boolean;
  outputSessionId: string | null;
};

export type Snapshot = { serverTime: number; devices: readonly Device[]; zones: readonly Zone[] };

/** 队列变化（合同 §2.4）；服务端以后增加的种类按 reload 处理。 */
export type QueueChange = { queueRevision: number } & (
  | { kind: "replace"; entries: readonly QueueEntry[] }
  | { kind: "reload" }
  | { kind: "insert"; index: number; entries: readonly QueueEntry[] }
  | { kind: "remove"; entryId: string }
  | { kind: "move"; entryId: string; delta: -1 | 1 }
  | { kind: "clear" }
);

export type ZoneQueue = { zoneId: string; queueRevision: number; entries: readonly QueueEntry[] };

/** 输出端认领时声明的能力（合同 §2.6）。 */
export type Capabilities = {
  stream_formats: { codec: string; mime_type: string; lossless: boolean; max_sample_rate: number | null; max_bit_depth: number | null }[];
  max_channels: number;
  dsd: "none" | "dop" | "native";
  exclusive: boolean;
  volume: "number" | "fixed";
};
