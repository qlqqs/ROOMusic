import { describe, expect, it } from "vitest";
import deviceEvent from "../../../../../backend/internal/app/testdata/api-samples/playback-zones/events/device.json";
import removedEvent from "../../../../../backend/internal/app/testdata/api-samples/playback-zones/events/device-removed.json";
import queueEvent from "../../../../../backend/internal/app/testdata/api-samples/playback-zones/events/queue.json";
import snapshotEvent from "../../../../../backend/internal/app/testdata/api-samples/playback-zones/events/snapshot.json";
import signalEvent from "../../../../../backend/internal/app/testdata/api-samples/playback-zones/events/zone-signal.json";
import nextStreamEvent from "../../../../../backend/internal/app/testdata/api-samples/playback-zones/events/zone-next-stream.json";
import failedTrackEvent from "../../../../../backend/internal/app/testdata/api-samples/playback-zones/events/zone-track-failed.json";
import { sampleBody } from "../../../test/api-samples";
import type { QueueEntry } from "../engine/queue";
import {
  decodeClaimResult,
  decodeCommandResult,
  decodeDeviceResult,
  decodeRegisterResult,
  decodeReportResult,
  decodeSnapshot,
  decodeStreamEvent,
  decodeZoneQueue,
  deviceDecoder,
  queueChangeDecoder,
  snapshotDecoder,
  zoneDecoder,
} from "./decoders";
import type { Device, Snapshot, Zone, ZoneStream } from "./model";

// 仅选取代表样本，不枚举样本目录。期望值独立写出，不调用被测解码器或曲目映射函数。
// 后端样本时间规范化为 2026-01-01T00:00:00Z；这里明确断言毫秒，不沿用字符串或秒数。
const sampleTime = 1_767_225_600_000;
const id = (suffix: number) => `00000000-0000-4000-8000-${String(suffix).padStart(12, "0")}`;
const response = (name: string) => sampleBody(`playback-zones/responses/${name}`);

const settings: Zone["settings"] = {
  leveling: "auto",
  gapless: true,
  crossfade: false,
  crossfadeSeconds: 5,
  quality: "lossless",
  headroom: { enabled: false, db: -3, clipIndicator: false },
  sampleRate: { strategy: "none", customRate: null, filter: "linear_precise" },
  downmix: { enabled: true },
  peq: { enabled: false, preampDb: 0, bands: [], source: null },
  convolution: { enabled: false, irId: null, beforePeq: false },
};

const registeredDevice: Device = {
  deviceId: id(1),
  zoneId: id(2),
  name: "样本设备",
  kind: "browser",
  online: false,
  lastSeenAt: sampleTime,
  volume: 0.5,
  muted: false,
  volumeControllable: true,
  outputSessionId: null,
};

const claimedDevice: Device = {
  ...registeredDevice,
  zoneId: id(3),
  name: "客厅",
  online: true,
  volume: 0.8,
  muted: true,
  outputSessionId: id(2),
};

function idleZone(overrides: Partial<Zone> = {}): Zone {
  return {
    zoneId: id(2),
    deviceId: id(1),
    revision: 0,
    queueRevision: 0,
    queueLength: 0,
    mode: "sequential",
    settings,
    current: null,
    hasNext: false,
    intent: "pause",
    status: "idle",
    fault: null,
    failures: {},
    position: { seconds: 0, at: sampleTime, durationSeconds: null, seekable: false },
    stream: null,
    nextStream: null,
    pausedAt: sampleTime,
    signal: null,
    event: null,
    ...overrides,
  };
}

function entry(entryId: number, trackId: number, title: string, groupId = 2, releaseId = 3): QueueEntry {
  return {
    entryId: id(entryId),
    track: {
      trackId: id(trackId),
      releaseId: id(releaseId),
      groupId: id(groupId),
      title,
      artist: "测试艺人",
      artistRefs: [],
      albumTitle: "合成发行",
      albumArtist: "测试艺人",
      durationSeconds: 20,
      artworkId: null,
      source: {
        sourceKind: "physical",
        codec: null,
        bitDepth: null,
        sampleRate: null,
        channels: null,
        bitrate: null,
      },
    },
  };
}

function stream(zoneId: number, seq: number, current: QueueEntry, trackStartSeconds = 0): ZoneStream {
  return {
    seq,
    url: `/api/v1/me/playback/zones/${id(zoneId)}/streams/${seq}`,
    mimeType: "audio/wav",
    quality: "lossless",
    format: { codec: "wav", sampleRate: 48000, bitDepth: 16, channels: 2 },
    segments: [{ ...current, streamStartSeconds: 0, trackStartSeconds }],
  };
}

const queueEntries = [
  entry(1, 4, "合成曲目 1"),
  entry(5, 6, "合成曲目 2"),
  entry(7, 8, "合成曲目 3"),
];
const claimedZone = idleZone({ zoneId: id(3), revision: 1 });
const snapshot: Snapshot = { serverTime: sampleTime, devices: [claimedDevice], zones: [claimedZone] };

const current = entry(1, 4, "合成曲目 1");
const nextStreamZone = idleZone({
  zoneId: id(8),
  deviceId: id(5),
  revision: 9,
  queueRevision: 1,
  queueLength: 3,
  current,
  hasNext: true,
  intent: "play",
  status: "playing",
  position: { seconds: 11, at: sampleTime, durationSeconds: 20, seekable: true },
  stream: stream(8, 2, current),
  nextStream: stream(8, 9, entry(6, 7, "合成曲目 2")),
  pausedAt: null,
  event: { kind: "play_recorded", seq: 8, trackId: id(4) },
});

const failedTrackZone = idleZone({
  zoneId: id(6),
  deviceId: id(5),
  revision: 38,
  queueRevision: 9,
  queueLength: 1,
  settings: { ...settings, crossfade: true, crossfadeSeconds: 8, leveling: "album" },
  current,
  intent: "play",
  status: "loading",
  failures: { [id(1)]: "decode_failed" },
  position: { seconds: 0, at: sampleTime, durationSeconds: 20, seekable: true },
  stream: { ...stream(6, 37, current), format: null },
  pausedAt: null,
  event: { kind: "track_failed", seq: 38, entryId: id(1), reason: "decode_failed" },
});

describe("播放区公开 DTO 的真实样本", () => {
  it("非默认 Headroom 设置样本保留开关、小数增益与削波指示", () => {
    expect(decodeCommandResult(response("set-settings-headroom")).zone.settings.headroom).toStrictEqual({
      enabled: true, db: -3.1, clipIndicator: true,
    });
  });

  it("播放区逐字段映射失败条目、事件、空格式流与时间毫秒", () => {
    expect(zoneDecoder(failedTrackEvent.data.zone, "$")).toStrictEqual(failedTrackZone);
  });

  it("设备逐字段映射身份、音量能力与最后在线时间毫秒", () => {
    expect(deviceDecoder(deviceEvent.data.device, "$")).toStrictEqual(registeredDevice);
  });

  it("快照底层解码器逐字段映射非空设备与播放区", () => {
    expect(snapshotDecoder(response("snapshot"), "$")).toStrictEqual(snapshot);
  });

  it("队列变化逐字段映射替换条目与队列版本", () => {
    expect(queueChangeDecoder(queueEvent.data.change, "$")).toStrictEqual({
      queueRevision: 1,
      kind: "replace",
      entries: queueEntries,
    });
  });

  it("快照响应逐字段映射服务器时间、设备与播放区", () => {
    expect(decodeSnapshot(response("snapshot"))).toStrictEqual(snapshot);
  });

  it("队列响应逐字段映射条目身份、曲目摘要与顺序", () => {
    expect(decodeZoneQueue(response("queue"))).toStrictEqual({
      zoneId: id(9),
      queueRevision: 1,
      entries: queueEntries,
    });
  });

  it("命令响应逐字段映射转移源、目标、队列变化与缺省导入结果", () => {
    expect(decodeCommandResult(response("transfer"))).toStrictEqual({
      serverTime: sampleTime,
      zone: idleZone({
        zoneId: id(8),
        deviceId: id(7),
        revision: 36,
        queueRevision: 8,
        settings: { ...settings, crossfade: true, crossfadeSeconds: 8, leveling: "album" },
      }),
      queueChange: { kind: "clear", queueRevision: 8 },
      targetZone: idleZone({
        zoneId: id(6),
        deviceId: id(5),
        revision: 2,
        queueRevision: 1,
        queueLength: 1,
        current,
        intent: "play",
        status: "loading",
        position: { seconds: 2, at: sampleTime, durationSeconds: 20, seekable: true },
        stream: stream(6, 2, current, 2),
        pausedAt: null,
        event: { kind: "track_started", seq: 2, entryId: id(1) },
      }),
      imported: null,
    });
  });

  it("设备响应逐字段映射改名结果与服务器时间毫秒", () => {
    expect(decodeDeviceResult(response("rename-device"))).toStrictEqual({
      serverTime: sampleTime,
      device: { ...registeredDevice, name: "客厅" },
    });
  });

  it("登记响应逐字段映射设备与初始暂停时间毫秒", () => {
    expect(decodeRegisterResult(response("register-device"))).toStrictEqual({
      serverTime: sampleTime,
      device: registeredDevice,
      zone: idleZone(),
    });
  });

  it("认领响应逐字段映射输出会话与在线设备", () => {
    expect(decodeClaimResult(response("claim"))).toStrictEqual({
      serverTime: sampleTime,
      sessionId: id(2),
      device: claimedDevice,
      zone: claimedZone,
    });
  });

  it("回报响应逐字段映射播放状态、当前曲目与流分段", () => {
    const reportedCurrent = entry(4, 7, "合成曲目 1", 5, 6);
    expect(decodeReportResult(response("status-playing"))).toStrictEqual({
      serverTime: sampleTime,
      device: claimedDevice,
      zone: idleZone({
        zoneId: id(3),
        revision: 4,
        queueRevision: 1,
        queueLength: 3,
        current: reportedCurrent,
        hasNext: true,
        intent: "play",
        status: "playing",
        position: { seconds: 0, at: sampleTime, durationSeconds: 20, seekable: true },
        stream: stream(3, 2, reportedCurrent),
        pausedAt: null,
        event: { kind: "track_started", seq: 2, entryId: id(4) },
      }),
    });
  });
});

describe("五种 SSE 事件的真实样本", () => {
  it("非空信号路径事件保留步骤、削波与处理速度", () => {
    const decoded = decodeStreamEvent(signalEvent.event, JSON.stringify(signalEvent.data));
    if (decoded?.type !== "zone") throw new Error("信号样本未解码为播放区事件");
    expect(decoded.zone.signal).toStrictEqual({
      quality: "enhanced", clipping: true, processingSpeed: 85.3,
      steps: [
        { kind: "source", quality: "lossless", lossy: false, compat: false, params: { codec: "wav", sampleRate: 48000, bitDepth: 16, channels: 2 } },
        { kind: "headroom", quality: "enhanced", lossy: false, compat: false, params: { gainDb: -3.1 } },
        { kind: "dither", quality: "enhanced", lossy: false, compat: false, params: { bits: 16, kind: "tpdf" } },
        { kind: "encode", quality: "lossless", lossy: false, compat: false, params: { codec: "wav", sampleRate: 48000, bitDepth: 16, bitrateKbps: null } },
      ],
    });
  });

  it("跳过未知步骤，保留前后已知步骤与服务端整体评级", () => {
    const signal = signalEvent.data.zone.signal;
    const zone = zoneDecoder({
      ...signalEvent.data.zone,
      signal: { ...signal, steps: [signal.steps[0], { kind: "future_step", quality: "enhanced", lossy: false, compat: false, params: {} }, ...signal.steps.slice(1)] },
    }, "$");
    expect(zone.signal?.steps.map((step) => step.kind)).toEqual(["source", "headroom", "dither", "encode"]);
    expect(zone.signal?.quality).toBe("enhanced");
  });

  it("缺失的削波、速度、来源位深与编码可选参数解成 null", () => {
    const zone = zoneDecoder({
      ...signalEvent.data.zone,
      signal: {
        quality: "low",
        steps: [
          { kind: "source", quality: "low", lossy: true, compat: false, params: { codec: "mp3", sample_rate: 44100, channels: 2 } },
          { kind: "encode", quality: "low", lossy: true, compat: false, params: { codec: "opus", sample_rate: 48000 } },
        ],
      },
    }, "$");
    expect(zone.signal).toStrictEqual({
      quality: "low", clipping: null, processingSpeed: null,
      steps: [
        { kind: "source", quality: "low", lossy: true, compat: false, params: { codec: "mp3", sampleRate: 44100, bitDepth: null, channels: 2 } },
        { kind: "encode", quality: "low", lossy: true, compat: false, params: { codec: "opus", sampleRate: 48000, bitDepth: null, bitrateKbps: null } },
      ],
    });
  });

  it("快照事件逐字段映射空列表与服务器时间毫秒", () => {
    expect(decodeStreamEvent(snapshotEvent.event, JSON.stringify(snapshotEvent.data))).toStrictEqual({
      type: "snapshot",
      snapshot: { serverTime: sampleTime, devices: [], zones: [] },
    });
  });

  it("播放区事件逐字段映射当前流、接续流与播放记录公告", () => {
    expect(decodeStreamEvent(nextStreamEvent.event, JSON.stringify(nextStreamEvent.data))).toStrictEqual({
      type: "zone",
      zone: nextStreamZone,
    });
  });

  it("队列事件逐字段映射播放区身份、版本与全部条目", () => {
    expect(decodeStreamEvent(queueEvent.event, JSON.stringify(queueEvent.data))).toStrictEqual({
      type: "queue",
      zoneId: id(9),
      change: { kind: "replace", queueRevision: 1, entries: queueEntries },
    });
  });

  it("设备事件逐字段映射设备身份、在线状态与音量能力", () => {
    expect(decodeStreamEvent(deviceEvent.event, JSON.stringify(deviceEvent.data))).toStrictEqual({
      type: "device",
      device: registeredDevice,
    });
  });

  it("删除设备事件逐字段映射设备与播放区身份", () => {
    expect(decodeStreamEvent(removedEvent.event, JSON.stringify(removedEvent.data))).toStrictEqual({
      type: "device_removed",
      deviceId: id(1),
      zoneId: id(2),
    });
  });
});
