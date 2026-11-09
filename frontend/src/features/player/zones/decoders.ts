// 播放区协议 v1 的边界解码（合同 §2–§5）。v1 内服务端只做增加：不认识的字段被忽略，不认识的枚举值按最接近的已知含义处理，
// 不认识的事件类型解成 null（界面不公告），不会让整份状态被拒绝。
import { decode as d } from "../../../shared/api";
import { trackSummaryDecoder } from "../../catalog";
import { playModes, MAX_QUEUE_ENTRIES, type QueueEntry } from "../engine/queue";
import { playerTrackOfSummary } from "../engine/tracks";
import { peqDocumentDecoder } from "../peq/document";
import {
  crossfadeDurations,
  downmixModes,
  levelingModes,
  playbackFailureReasons,
  sampleRateFilters,
  sampleRateStrategies,
  signalQualities,
  streamQualities,
  type Capabilities,
  type CrossfadeSeconds,
  type Device,
  type PlaybackFailureReason,
  type PlaybackSettings,
  type PlayerEvent,
  type PlayerFault,
  type QueueChange,
  type Signal,
  type SignalStep,
  type Snapshot,
  type Zone,
  type ZoneQueue,
  type ZoneStream,
} from "./model";

/** 段数、失败标记与事件流里一次 replace 的上限，只防异常响应被整体接受。 */
const MAX_SEGMENTS = 256;
const MAX_DEVICES = 256;

function time(): d.Decoder<number> {
  const inner = d.timestamp();
  return (input, path) => Date.parse(inner(input, path));
}

/** 已知取值之一；不认识的值换成 fallback（v1 允许新增枚举值）。 */
function lenient<const V extends readonly string[]>(values: V, fallback: V[number]): d.Decoder<V[number]> {
  return (input, path) => {
    const text = d.string({ max: 64 })(input, path);
    return values.find((value) => value === text) ?? fallback;
  };
}

const signalQualityDecoder = lenient(signalQualities, "low");
const sampleRateFilterDecoder = lenient(sampleRateFilters, "linear_precise");

/** 信号路径的一步（dsp-pipeline 设计 §9.1）；不认识的种类是 null，整步不显示。 */
const signalStepDecoder: d.Decoder<SignalStep | null> = (input, path) => {
  const head = d.object({ kind: d.string({ max: 64 }), quality: signalQualityDecoder, lossy: d.boolean(), compat: d.boolean() })(input, path);
  const base = { quality: head.quality, lossy: head.lossy, compat: head.compat };
  const paramsOf = <T,>(params: d.Decoder<T>): T => d.object({ params })(input, path).params;
  switch (head.kind) {
    case "source": {
      const raw = paramsOf(d.object({ codec: d.string({ max: 64 }), sample_rate: d.integer({ min: 0 }), bit_depth: d.optional(d.integer({ min: 0 })), channels: d.integer({ min: 0 }) }));
      return { ...base, kind: "source", params: { codec: raw.codec, sampleRate: raw.sample_rate, bitDepth: raw.bit_depth ?? null, channels: raw.channels } };
    }
    case "convert": {
      const raw = paramsOf(d.object({ from_rate: d.integer({ min: 0 }), to_rate: d.integer({ min: 0 }), by: d.string({ max: 64 }) }));
      return { ...base, kind: "convert", params: { fromRate: raw.from_rate, toRate: raw.to_rate, by: raw.by } };
    }
    case "sample_rate": {
      const raw = paramsOf(d.object({ from_rate: d.integer({ min: 0 }), to_rate: d.integer({ min: 0 }), filter: sampleRateFilterDecoder }));
      return { ...base, kind: "sample_rate", params: { fromRate: raw.from_rate, toRate: raw.to_rate, filter: raw.filter } };
    }
    case "downmix": {
      const raw = paramsOf(d.object({ from_channels: d.integer({ min: 0 }), to_channels: d.integer({ min: 0 }), from_layout: d.optional(d.string({ max: 64 })), to_layout: d.optional(d.string({ max: 64 })), by: d.string({ max: 64 }), mode: d.optional(lenient(downmixModes, "matrix")) }));
      return { ...base, kind: "downmix", params: { fromChannels: raw.from_channels, toChannels: raw.to_channels, fromLayout: raw.from_layout ?? null, toLayout: raw.to_layout ?? null, by: raw.by, mode: raw.mode ?? null } };
    }
    case "peq": {
      const raw = paramsOf(d.object({ label: d.string({ max: 512 }), bands: d.integer({ min: 0 }) }));
      return { ...base, kind: "peq", params: { label: raw.label, bands: raw.bands } };
    }
    case "leveling": {
      const raw = paramsOf(d.object({ gain_db: d.number(), basis: d.string({ max: 64 }) }));
      return { ...base, kind: "leveling", params: { gainDb: raw.gain_db, basis: raw.basis } };
    }
    case "headroom":
      return { ...base, kind: "headroom", params: { gainDb: paramsOf(d.object({ gain_db: d.number() })).gain_db } };
    case "dither": {
      const raw = paramsOf(d.object({ bits: d.integer({ min: 0 }), kind: d.string({ max: 64 }) }));
      return { ...base, kind: "dither", params: { bits: raw.bits, kind: raw.kind } };
    }
    case "encode": {
      const raw = paramsOf(d.object({ codec: d.string({ max: 64 }), sample_rate: d.integer({ min: 0 }), bit_depth: d.optional(d.integer({ min: 0 })), bitrate_kbps: d.optional(d.integer({ min: 0 })) }));
      return { ...base, kind: "encode", params: { codec: raw.codec, sampleRate: raw.sample_rate, bitDepth: raw.bit_depth ?? null, bitrateKbps: raw.bitrate_kbps ?? null } };
    }
    default:
      return null;
  }
};

const signalDecoder: d.Decoder<Signal> = (input, path) => {
  const raw = d.object({
    quality: signalQualityDecoder,
    steps: d.array(signalStepDecoder, { max: 64 }),
    clipping: d.optional(d.boolean()),
    processing_speed: d.optional(d.number({ min: 0 })),
  })(input, path);
  return { quality: raw.quality, steps: raw.steps.filter((step) => step !== null), clipping: raw.clipping ?? null, processingSpeed: raw.processing_speed ?? null };
};

const queueEntryDecoder: d.Decoder<QueueEntry> = (input, path) => {
  const raw = d.object({ entry_id: d.uuid(), track: trackSummaryDecoder })(input, path);
  return { entryId: raw.entry_id, track: playerTrackOfSummary(raw.track) };
};

const crossfadeSecondsDecoder: d.Decoder<CrossfadeSeconds> = (input, path) => {
  const value = d.integer()(input, path);
  const match = crossfadeDurations.find((seconds) => seconds === value);
  if (match === undefined) throw new d.DecodeError(path, "不是可选的渐变时长");
  return match;
};

const settingsDecoder: d.Decoder<PlaybackSettings> = (input, path) => {
  const raw = d.object({
    leveling: lenient(levelingModes, "auto"),
    gapless: d.boolean(),
    crossfade: d.boolean(),
    crossfade_seconds: crossfadeSecondsDecoder,
    quality: lenient(streamQualities, "lossless"),
    headroom: d.object({ enabled: d.boolean(), db: d.number(), clip_indicator: d.boolean() }),
    sample_rate: d.object({ strategy: lenient(sampleRateStrategies, "none"), custom_rate: d.nullable(d.integer({ min: 0 })), filter: sampleRateFilterDecoder }),
    downmix: d.object({ enabled: d.boolean() }),
    peq: peqDocumentDecoder,
  })(input, path);
  return {
    leveling: raw.leveling,
    gapless: raw.gapless,
    crossfade: raw.crossfade,
    crossfadeSeconds: raw.crossfade_seconds,
    quality: raw.quality,
    headroom: { enabled: raw.headroom.enabled, db: raw.headroom.db, clipIndicator: raw.headroom.clip_indicator },
    sampleRate: { strategy: raw.sample_rate.strategy, customRate: raw.sample_rate.custom_rate, filter: raw.sample_rate.filter },
    downmix: { enabled: raw.downmix.enabled },
    peq: raw.peq,
  };
};

const failureReasonDecoder = lenient(playbackFailureReasons, "source_unavailable");

const streamDecoder: d.Decoder<ZoneStream> = (input, path) => {
  const raw = d.object({
    seq: d.integer({ min: 0 }),
    // 地址由服务端给出、交给媒体元素直接请求：只接受本站的 API 路径。
    url: d.string({ max: 300, pattern: /^\/api\/v1\/[A-Za-z0-9/_.~%-]+(?:\?[A-Za-z0-9=&_.~%-]*)?$/ }),
    mime_type: d.string({ min: 1, max: 100 }),
    quality: lenient(streamQualities, "lossless"),
    format: d.nullable(d.object({ codec: d.string({ min: 1, max: 64 }), sample_rate: d.integer({ min: 0 }), bit_depth: d.nullable(d.integer({ min: 0 })), channels: d.integer({ min: 0 }) })),
    segments: d.array(
      d.object({ entry_id: d.uuid(), track: trackSummaryDecoder, stream_start_seconds: d.number({ min: 0 }), track_start_seconds: d.number({ min: 0 }) }),
      { max: MAX_SEGMENTS },
    ),
  })(input, path);
  return {
    seq: raw.seq,
    url: raw.url,
    mimeType: raw.mime_type,
    quality: raw.quality,
    format: raw.format === null ? null : { codec: raw.format.codec, sampleRate: raw.format.sample_rate, bitDepth: raw.format.bit_depth, channels: raw.format.channels },
    segments: raw.segments.map((segment) => ({
      entryId: segment.entry_id,
      track: playerTrackOfSummary(segment.track),
      streamStartSeconds: segment.stream_start_seconds,
      trackStartSeconds: segment.track_start_seconds,
    })),
  };
};

const faultDecoder: d.Decoder<PlayerFault> = (input, path) => {
  const raw = d.object({ kind: lenient(["transient", "autoplay_blocked", "no_stream_format"] as const, "transient") })(input, path);
  return { kind: raw.kind };
};

/** 播放区事件：不认识的种类是 null。 */
const eventDecoder: d.Decoder<PlayerEvent | null> = (input, path) => {
  const head = d.object({ seq: d.integer({ min: 0 }), kind: d.string({ max: 64 }) })(input, path);
  const { seq } = head;
  switch (head.kind) {
    case "track_started":
      return { seq, kind: "track_started", entryId: d.object({ entry_id: d.uuid() })(input, path).entry_id };
    case "track_failed": {
      const raw = d.object({ entry_id: d.uuid(), reason: failureReasonDecoder })(input, path);
      return { seq, kind: "track_failed", entryId: raw.entry_id, reason: raw.reason };
    }
    case "queue_restored": {
      const raw = d.object({ restored: d.integer({ min: 0 }), dropped: d.integer({ min: 0 }) })(input, path);
      return { seq, kind: "queue_restored", restored: raw.restored, dropped: raw.dropped };
    }
    case "play_recorded":
      return { seq, kind: "play_recorded", trackId: d.object({ track_id: d.uuid() })(input, path).track_id };
    case "all_failed":
    case "failures_stopped":
    case "transient_failure":
    case "autoplay_blocked":
    case "no_stream_format":
      return { seq, kind: head.kind };
    default:
      return null;
  }
};

export const zoneDecoder: d.Decoder<Zone> = (input, path) => {
  const raw = d.object({
    zone_id: d.uuid(),
    device_id: d.uuid(),
    revision: d.integer({ min: 0 }),
    queue_revision: d.integer({ min: 0 }),
    queue_length: d.integer({ min: 0, max: MAX_QUEUE_ENTRIES }),
    mode: lenient(playModes, "sequential"),
    settings: settingsDecoder,
    current: d.nullable(queueEntryDecoder),
    has_next: d.boolean(),
    intent: lenient(["play", "pause"] as const, "pause"),
    status: lenient(["idle", "loading", "playing", "paused", "buffering"] as const, "paused"),
    fault: d.nullable(faultDecoder),
    failures: d.array(d.object({ entry_id: d.uuid(), reason: failureReasonDecoder }), { max: MAX_QUEUE_ENTRIES }),
    position: d.object({ seconds: d.number({ min: 0 }), at: time(), duration_seconds: d.nullable(d.number({ min: 0 })), seekable: d.boolean() }),
    stream: d.nullable(streamDecoder),
    next_stream: d.nullable(streamDecoder),
    paused_at: d.nullable(time()),
    signal: d.nullable(signalDecoder),
    event: d.nullable(eventDecoder),
  })(input, path);
  const failures: Record<string, PlaybackFailureReason> = {};
  for (const failure of raw.failures) failures[failure.entry_id] = failure.reason;
  return {
    zoneId: raw.zone_id,
    deviceId: raw.device_id,
    revision: raw.revision,
    queueRevision: raw.queue_revision,
    queueLength: raw.queue_length,
    mode: raw.mode,
    settings: raw.settings,
    current: raw.current,
    hasNext: raw.has_next,
    intent: raw.intent,
    status: raw.status,
    fault: raw.fault,
    failures,
    position: { seconds: raw.position.seconds, at: raw.position.at, durationSeconds: raw.position.duration_seconds, seekable: raw.position.seekable },
    stream: raw.stream,
    nextStream: raw.next_stream,
    pausedAt: raw.paused_at,
    signal: raw.signal,
    event: raw.event,
  };
};

const capabilitiesVolumeDecoder: d.Decoder<Capabilities["volume"]> = lenient(["number", "fixed"] as const, "number");

export const deviceDecoder: d.Decoder<Device> = (input, path) => {
  const raw = d.object({
    device_id: d.uuid(),
    zone_id: d.uuid(),
    name: d.string({ min: 1, max: 64 }),
    kind: d.string({ min: 1, max: 32 }),
    online: d.boolean(),
    last_seen_at: time(),
    volume: d.number({ min: 0, max: 1 }),
    muted: d.boolean(),
    capabilities: d.object({ volume: capabilitiesVolumeDecoder }),
    output_session_id: d.nullable(d.uuid()),
  })(input, path);
  return {
    deviceId: raw.device_id,
    zoneId: raw.zone_id,
    name: raw.name,
    kind: raw.kind,
    online: raw.online,
    lastSeenAt: raw.last_seen_at,
    volume: raw.volume,
    muted: raw.muted,
    volumeControllable: raw.capabilities.volume !== "fixed",
    outputSessionId: raw.output_session_id,
  };
};

export const snapshotDecoder: d.Decoder<Snapshot> = (input, path) => {
  const raw = d.object({
    server_time: time(),
    protocol: d.integer({ min: 1 }),
    devices: d.array(deviceDecoder, { max: MAX_DEVICES }),
    zones: d.array(zoneDecoder, { max: MAX_DEVICES }),
  })(input, path);
  return { serverTime: raw.server_time, devices: raw.devices, zones: raw.zones };
};

const entriesDecoder = d.array(queueEntryDecoder, { max: MAX_QUEUE_ENTRIES });

export const queueChangeDecoder: d.Decoder<QueueChange> = (input, path) => {
  const head = d.object({ queue_revision: d.integer({ min: 0 }), kind: d.string({ max: 64 }) })(input, path);
  const queueRevision = head.queue_revision;
  switch (head.kind) {
    case "replace":
      return { queueRevision, kind: "replace", entries: d.object({ entries: entriesDecoder })(input, path).entries };
    case "insert": {
      const raw = d.object({ index: d.integer({ min: 0 }), entries: entriesDecoder })(input, path);
      return { queueRevision, kind: "insert", index: raw.index, entries: raw.entries };
    }
    case "remove":
      return { queueRevision, kind: "remove", entryId: d.object({ entry_id: d.uuid() })(input, path).entry_id };
    case "move": {
      const raw = d.object({ entry_id: d.uuid(), delta: d.integer({ min: -1, max: 1 }) })(input, path);
      if (raw.delta === 0) throw new d.DecodeError(`${path}.delta`, "应为 -1 或 1");
      return { queueRevision, kind: "move", entryId: raw.entry_id, delta: raw.delta === -1 ? -1 : 1 };
    }
    case "clear":
      return { queueRevision, kind: "clear" };
    default:
      return { queueRevision, kind: "reload" };
  }
};

const zoneQueueDecoder: d.Decoder<ZoneQueue> = (input, path) => {
  const raw = d.object({ zone_id: d.uuid(), queue_revision: d.integer({ min: 0 }), entries: entriesDecoder })(input, path);
  const ids = new Set(raw.entries.map((entry) => entry.entryId));
  if (ids.size !== raw.entries.length) throw new d.DecodeError(`${path}.entries`, "条目 ID 重复");
  return { zoneId: raw.zone_id, queueRevision: raw.queue_revision, entries: raw.entries };
};

export type CommandResult = { serverTime: number; zone: Zone; queueChange: QueueChange | null; targetZone: Zone | null; imported: boolean | null };
export type DeviceResult = { serverTime: number; device: Device };
export type RegisterResult = { serverTime: number; device: Device; zone: Zone };
export type ClaimResult = { serverTime: number; sessionId: string; device: Device; zone: Zone };
export type ReportResult = { serverTime: number; device: Device; zone: Zone };

const commandResultDecoder: d.Decoder<CommandResult> = (input, path) => {
  const raw = d.object({
    server_time: time(),
    zone: zoneDecoder,
    queue_change: d.nullable(queueChangeDecoder),
    target_zone: d.optional(zoneDecoder),
    imported: d.optional(d.boolean()),
  })(input, path);
  return { serverTime: raw.server_time, zone: raw.zone, queueChange: raw.queue_change, targetZone: raw.target_zone ?? null, imported: raw.imported ?? null };
};

const deviceResultDecoder: d.Decoder<DeviceResult> = (input, path) => {
  const raw = d.object({ server_time: time(), device: deviceDecoder })(input, path);
  return { serverTime: raw.server_time, device: raw.device };
};

const deviceZoneDecoder: d.Decoder<RegisterResult> = (input, path) => {
  const raw = d.object({ server_time: time(), device: deviceDecoder, zone: zoneDecoder })(input, path);
  return { serverTime: raw.server_time, device: raw.device, zone: raw.zone };
};

const claimResultDecoder: d.Decoder<ClaimResult> = (input, path) => {
  const raw = d.object({ server_time: time(), protocol: d.literal(1), session_id: d.uuid(), device: deviceDecoder, zone: zoneDecoder })(input, path);
  return { serverTime: raw.server_time, sessionId: raw.session_id, device: raw.device, zone: raw.zone };
};

export const decodeSnapshot = d.defineDecoder(snapshotDecoder);
export const decodeZoneQueue = d.defineDecoder(zoneQueueDecoder);
export const decodeCommandResult = d.defineDecoder(commandResultDecoder);
export const decodeDeviceResult = d.defineDecoder(deviceResultDecoder);
export const decodeRegisterResult = d.defineDecoder(deviceZoneDecoder);
export const decodeClaimResult = d.defineDecoder(claimResultDecoder);
export const decodeReportResult = d.defineDecoder(deviceZoneDecoder);

/** 事件流里的一条事件（合同 §5）；不认识的事件类型是 null。 */
export type StreamEvent =
  | { type: "snapshot"; snapshot: Snapshot }
  | { type: "zone"; zone: Zone }
  | { type: "queue"; zoneId: string; change: QueueChange }
  | { type: "device"; device: Device }
  | { type: "device_removed"; deviceId: string; zoneId: string };

/** 解码一条事件的 data（一行 JSON）。JSON 不合法或形状不符时抛出。 */
export function decodeStreamEvent(type: string, data: string): StreamEvent | null {
  const input: unknown = JSON.parse(data);
  switch (type) {
    case "snapshot":
      return { type, snapshot: snapshotDecoder(input, "$") };
    case "zone":
      return { type, zone: d.object({ zone: zoneDecoder })(input, "$").zone };
    case "queue": {
      const raw = d.object({ zone_id: d.uuid(), change: queueChangeDecoder })(input, "$");
      return { type, zoneId: raw.zone_id, change: raw.change };
    }
    case "device":
      return { type, device: d.object({ device: deviceDecoder })(input, "$").device };
    case "device_removed": {
      const raw = d.object({ device_id: d.uuid(), zone_id: d.uuid() })(input, "$");
      return { type, deviceId: raw.device_id, zoneId: raw.zone_id };
    }
    default:
      return null;
  }
}
