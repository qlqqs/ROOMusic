import { defaultPeq, peqDocumentDecoder } from "../features/player/peq/document";
import { act, waitFor } from "@testing-library/react";
import { expect } from "vitest";
import { ApiError } from "../shared/api";
import { MAX_QUEUE_ENTRIES, type PlayerTrack, type QueueEntry } from "../features/player/engine/queue";
import type { ClaimBody, ReportBody, ZoneCommand } from "../features/player/zones/api";
import { PlayerController, type PlayerControllerDeps, type ZoneApi } from "../features/player/zones/controller";
import type { CommandResult } from "../features/player/zones/decoders";
import type { Device, PlaybackSettings, Zone, ZoneStream } from "../features/player/zones/model";
import { FakeAudio } from "./fake-audio";
import { memoryStorage } from "./memory-storage";
import { playerTrack } from "./player-data";

/**
 * 播放区服务端的内存替身（只给前端测试用）：一台设备、一个播放区，按合同 §3.3 的语义执行命令（简化：起播即开一条流、状态 loading，
 * 输出端回报的状态写回播放区；不模拟渲染会话的事件）。曲目只凭 ID 进队列，trackOf 把 ID 还原成显示用的曲目；默认把 track-<名字> 还原成 playerTrack(名字)。
 */
export type FakeZoneServer = {
  readonly api: ZoneApi;
  readonly commands: ZoneCommand[];
  readonly claims: ClaimBody[];
  readonly reports: ReportBody[];
  readonly released: number;
  zone: () => Zone;
  device: () => Device;
  queueIds: () => string[];
  /** 直接改服务端的播放区状态（例如渲染会话推进了位置）；客户端在下一次响应里看到。 */
  update: (patch: Partial<Zone>) => void;
  /** 让下一条命令失败（ApiError http）。 */
  failNext: (status: number, code: string) => void;
  /** 记下曲目的显示信息：之后按 ID 进队列的条目用它（testPlayerController 在每次入队前自动登记）。 */
  register: (tracks: readonly PlayerTrack[]) => void;
  /** 下一条命令执行后再改这些字段（例如模拟渲染器不可用的 transient 故障、某一项失败）。 */
  afterNext: (patch: Partial<Zone>) => void;
};

const DEVICE_ID = "00000000-0000-4000-8000-00000000d001";
const ZONE_ID = "00000000-0000-4000-8000-00000000e001";

const defaultSettings: PlaybackSettings = { leveling: "auto", gapless: true, crossfade: false, crossfadeSeconds: 5, quality: "lossless", headroom: { enabled: false, db: -3, clipIndicator: false }, sampleRate: { strategy: "none", customRate: null, filter: "linear_precise" }, downmix: { enabled: true }, peq: defaultPeq };

function defaultTrackOf(trackId: string): PlayerTrack {
  return trackId.startsWith("track-") ? playerTrack(trackId.slice("track-".length)) : playerTrack(trackId, { trackId });
}

export function fakeZoneServer(options: { trackOf?: (trackId: string) => PlayerTrack } = {}): FakeZoneServer {
  const known = new Map<string, PlayerTrack>();
  const trackOf = (trackId: string) => known.get(trackId) ?? (options.trackOf ?? defaultTrackOf)(trackId);
  const commands: ZoneCommand[] = [];
  const claims: ClaimBody[] = [];
  const reports: ReportBody[] = [];
  let released = 0;
  let entryCounter = 0;
  let failure: { status: number; code: string } | null = null;
  let after: Partial<Zone> | null = null;
  let entries: QueueEntry[] = [];
  let imported = false;
  let device: Device = { deviceId: DEVICE_ID, zoneId: ZONE_ID, name: "测试设备", kind: "browser", online: false, lastSeenAt: Date.now(), volume: 1, muted: false, volumeControllable: true, outputSessionId: null };
  let zone: Zone = {
    zoneId: ZONE_ID, deviceId: DEVICE_ID, revision: 1, queueRevision: 0, queueLength: 0, mode: "sequential", settings: defaultSettings,
    current: null, hasNext: false, intent: "pause", status: "idle", fault: null, failures: {},
    position: { seconds: 0, at: Date.now(), durationSeconds: null, seekable: false }, stream: null, nextStream: null, pausedAt: null, signal: null, event: null,
  };

  const newEntries = (trackIds: readonly string[]): QueueEntry[] => trackIds.map((trackId) => ({ entryId: `entry-${++entryCounter}`, track: trackOf(trackId) }));
  const index = (entryId: string | null) => entries.findIndex((entry) => entry.entryId === entryId);
  const streamOf = (entry: QueueEntry, seconds: number): ZoneStream => ({
    seq: zone.revision, url: `/api/v1/me/playback/zones/${ZONE_ID}/streams/${zone.revision}`, mimeType: "audio/wav", quality: "lossless",
    format: { codec: "wav", sampleRate: 44100, bitDepth: 16, channels: 2 },
    segments: [{ entryId: entry.entryId, track: entry.track, streamStartSeconds: 0, trackStartSeconds: seconds }],
  });
  const commit = (patch: Partial<Zone>, queueChanged: boolean) => {
    const revision = zone.revision + 1;
    const current = patch.current !== undefined ? patch.current : zone.current;
    const at = index(current?.entryId ?? null);
    const mode = patch.mode ?? zone.mode;
    zone = {
      ...zone, ...patch, revision,
      queueRevision: queueChanged ? zone.queueRevision + 1 : zone.queueRevision,
      queueLength: entries.length,
      hasNext: entries.length > 0 && (mode === "repeat_all" || mode === "shuffle" || at < entries.length - 1),
    };
  };
  /** 起播：意图播放、开一条从 seconds 起的流。 */
  const start = (entry: QueueEntry, seconds: number) => {
    zone = { ...zone, revision: zone.revision + 1 };
    commit({
      current: entry, intent: "play", status: "loading", fault: null, pausedAt: null, stream: streamOf(entry, seconds),
      position: { seconds, at: Date.now(), durationSeconds: entry.track.durationSeconds, seekable: entry.track.durationSeconds !== null },
      event: { seq: zone.revision + 1, kind: "track_started", entryId: entry.entryId },
    }, false);
  };
  const pause = (seconds = zone.position.seconds) => ({ intent: "pause" as const, status: zone.current === null ? "idle" as const : "paused" as const, pausedAt: Date.now(), position: { ...zone.position, seconds, at: Date.now() } });
  const idle = () => ({ current: null, intent: "pause" as const, status: "idle" as const, stream: null, pausedAt: Date.now(), position: { seconds: 0, at: Date.now(), durationSeconds: null, seekable: false } });
  const full = (count: number) => entries.length + count > MAX_QUEUE_ENTRIES;

  const execute = (command: ZoneCommand): Pick<CommandResult, "imported"> & { queueChanged: boolean } => {
    switch (command.command) {
      case "play_tracks": {
        entries = newEntries(command.track_ids);
        const first = entries[command.start_index] ?? entries[0];
        zone = { ...zone, failures: {} };
        if (first === undefined) commit(idle(), true);
        else {
          commit({}, true);
          start(first, 0);
        }
        return { queueChanged: true, imported: null };
      }
      case "play_next":
      case "enqueue": {
        if (full(command.track_ids.length)) throw new ApiError({ kind: "http", status: 409, code: "queue_full", message: "队列已满", requestId: "fake" });
        const added = newEntries(command.track_ids);
        const at = command.command === "enqueue" ? entries.length : index(zone.current?.entryId ?? null) + 1;
        entries = [...entries.slice(0, at), ...added, ...entries.slice(at)];
        const current = zone.current ?? added[0] ?? null;
        commit(zone.current === null ? { current, status: "paused", position: { seconds: 0, at: Date.now(), durationSeconds: current?.track.durationSeconds ?? null, seekable: current?.track.durationSeconds != null } } : {}, true);
        return { queueChanged: true, imported: null };
      }
      case "play_entry": {
        const entry = entries[index(command.entry_id)];
        if (entry !== undefined) start(entry, entry.entryId === zone.current?.entryId ? zone.position.seconds : 0);
        return { queueChanged: false, imported: null };
      }
      case "remove_entry": {
        const at = index(command.entry_id);
        if (at < 0) return { queueChanged: false, imported: null };
        const removedCurrent = zone.current?.entryId === command.entry_id;
        entries = entries.filter((entry) => entry.entryId !== command.entry_id);
        const successor = entries[at] ?? null;
        if (!removedCurrent) commit({}, true);
        else if (successor === null) commit(idle(), true);
        else if (zone.intent === "play") {
          commit({}, true);
          start(successor, 0);
        } else commit({ current: successor, stream: null, position: { seconds: 0, at: Date.now(), durationSeconds: successor.track.durationSeconds, seekable: successor.track.durationSeconds !== null } }, true);
        return { queueChanged: true, imported: null };
      }
      case "move_entry": {
        const at = index(command.entry_id);
        const to = at + command.delta;
        const moving = entries[at];
        const other = entries[to];
        if (moving === undefined || other === undefined) return { queueChanged: false, imported: null };
        entries = [...entries];
        entries[at] = other;
        entries[to] = moving;
        commit({}, true);
        return { queueChanged: true, imported: null };
      }
      case "clear_queue":
        entries = [];
        commit({ ...idle(), failures: {} }, true);
        return { queueChanged: true, imported: null };
      case "set_mode":
        commit({ mode: command.mode }, false);
        return { queueChanged: false, imported: null };
      case "play": {
        const entry = zone.current ?? entries[0];
        if (entry !== undefined) start(entry, zone.current === null ? 0 : zone.position.seconds);
        return { queueChanged: false, imported: null };
      }
      case "pause":
        commit(pause(), false);
        return { queueChanged: false, imported: null };
      case "stop":
        commit({ ...pause(0), stream: null }, false);
        return { queueChanged: false, imported: null };
      case "next":
      case "previous": {
        const at = index(zone.current?.entryId ?? null);
        const step = command.command === "next" ? 1 : -1;
        const target = entries[at + step] ?? (zone.mode === "repeat_all" ? entries[step === 1 ? 0 : entries.length - 1] : command.command === "previous" ? zone.current : undefined);
        if (target !== undefined && target !== null) start(target, 0);
        return { queueChanged: false, imported: null };
      }
      case "seek":
        commit({ position: { ...zone.position, seconds: command.position_seconds, at: Date.now() }, stream: zone.current === null || zone.stream === null ? zone.stream : streamOf(zone.current, command.position_seconds) }, false);
        return { queueChanged: false, imported: null };
      case "set_settings": {
        const patch = command.settings;
        commit({ settings: { ...zone.settings, ...(patch.leveling === undefined ? {} : { leveling: patch.leveling }), ...(patch.gapless === undefined ? {} : { gapless: patch.gapless }), ...(patch.crossfade === undefined ? {} : { crossfade: patch.crossfade }), ...(patch.crossfade_seconds === undefined ? {} : { crossfadeSeconds: patch.crossfade_seconds }), ...(patch.quality === undefined ? {} : { quality: patch.quality }), ...(patch.headroom === undefined ? {} : { headroom: { enabled: patch.headroom.enabled, db: patch.headroom.db, clipIndicator: patch.headroom.clip_indicator } }), ...(patch.sample_rate === undefined ? {} : { sampleRate: { strategy: patch.sample_rate.strategy, customRate: patch.sample_rate.custom_rate, filter: patch.sample_rate.filter } }), ...(patch.downmix === undefined ? {} : { downmix: { enabled: patch.downmix.enabled } }), ...(patch.peq === undefined ? {} : { peq: peqDocumentDecoder(patch.peq, "$.peq") }) } }, false);
        return { queueChanged: false, imported: null };
      }
      case "retry": {
        if (zone.fault?.kind === "transient" && zone.current !== null) start(zone.current, zone.position.seconds);
        return { queueChanged: false, imported: null };
      }
      case "transfer":
        throw new ApiError({ kind: "http", status: 400, code: "invalid_input", message: "替身只有一个播放区", requestId: "fake" });
      case "import": {
        if (imported || entries.length > 0) return { queueChanged: false, imported: false };
        imported = true;
        const mapped = command.entries.map((entry) => ({ old: entry.entry_id, entry: newEntries([entry.track_id])[0] }));
        entries = mapped.flatMap(({ entry }) => (entry === undefined ? [] : [entry]));
        const current = mapped.find(({ old }) => old === command.current_entry_id)?.entry ?? entries[0] ?? null;
        commit({ current, mode: command.mode, status: current === null ? "idle" : "paused", position: { seconds: command.position_seconds, at: Date.now(), durationSeconds: current?.track.durationSeconds ?? null, seekable: current?.track.durationSeconds != null }, event: { seq: zone.revision + 1, kind: "queue_restored", restored: entries.length, dropped: 0 } }, true);
        return { queueChanged: true, imported: true };
      }
    }
  };

  const api: ZoneApi = {
    fetchSnapshot: async () => ({ serverTime: Date.now(), devices: [device], zones: [zone] }),
    fetchZoneQueue: async () => ({ zoneId: ZONE_ID, queueRevision: zone.queueRevision, entries }),
    registerDevice: async () => ({ serverTime: Date.now(), device, zone }),
    sendZoneCommand: async (_zoneId, command) => {
      commands.push(command);
      if (failure !== null) {
        const { status, code } = failure;
        failure = null;
        throw new ApiError({ kind: "http", status, code, message: code, requestId: "fake" });
      }
      const { queueChanged, imported: result } = execute(command);
      if (after !== null) {
        commit(after, false);
        after = null;
      }
      return { serverTime: Date.now(), zone, queueChange: queueChanged ? { queueRevision: zone.queueRevision, kind: "replace", entries } : null, targetZone: null, imported: result };
    },
    putDeviceVolume: async (_deviceId, volume, muted) => {
      device = { ...device, volume, muted };
      return { serverTime: Date.now(), device };
    },
    renameDevice: async (_deviceId, name) => {
      device = { ...device, name };
      return { serverTime: Date.now(), device };
    },
    deleteDevice: async () => {
      if (device.online) throw new ApiError({ kind: "http", status: 409, code: "device_online", message: "设备在线", requestId: "fake" });
    },
    claimOutput: async (_deviceId, body) => {
      claims.push(body);
      device = { ...device, online: true, outputSessionId: "00000000-0000-4000-8000-00000000f001", volumeControllable: body.capabilities.volume !== "fixed" };
      return { serverTime: Date.now(), sessionId: "00000000-0000-4000-8000-00000000f001", device, zone };
    },
    postOutputReport: async (_sessionId, body) => {
      reports.push(body);
      if (body.kind === "fault" && body.fault === "autoplay_blocked") commit({ ...pause(), fault: { kind: "autoplay_blocked" }, event: { seq: zone.revision + 1, kind: "autoplay_blocked" } }, false);
      else if (body.kind === "status" && body.stream_seq === zone.stream?.seq && body.status !== zone.status) commit({ status: body.status }, false);
      return { serverTime: Date.now(), device, zone };
    },
    releaseOutput: () => {
      released += 1;
    },
    // 一条一直开着、什么也不推的事件流：替身的状态变化都经命令与回报的响应回到客户端。
    openEventStream: async () => new Response(new ReadableStream<Uint8Array>(), { status: 200, headers: { "Content-Type": "text/event-stream" } }),
  };

  return {
    api,
    commands,
    claims,
    reports,
    get released() {
      return released;
    },
    zone: () => zone,
    device: () => device,
    queueIds: () => entries.map((entry) => entry.track.trackId),
    update: (patch) => commit(patch, false),
    failNext: (status, code) => {
      failure = { status, code };
    },
    register: (tracks) => {
      for (const track of tracks) known.set(track.trackId, track);
    },
    afterNext: (patch) => {
      after = patch;
    },
  };
}

/**
 * 接在替身上的控制器。audio 为 true 时本“标签页”出声（没有 Web Locks，直接认领），创建的音频元素按顺序放进 audios。
 */
export function testPlayerController(server: FakeZoneServer, options: { audio?: boolean; deps?: Partial<PlayerControllerDeps> } = {}) {
  const audios: FakeAudio[] = [];
  const controller = new PlayerController({
    userId: "00000000-0000-4000-8000-000000000003",
    api: server.api,
    localStorage: memoryStorage({ "roomusic:playback-device:v1:00000000-0000-4000-8000-000000000003": JSON.stringify({ version: 1, deviceId: DEVICE_ID }) }),
    sessionStorage: null,
    locks: null,
    createAudio: options.audio === true ? () => {
      const audio = new FakeAudio();
      audios.push(audio);
      return audio;
    } : null,
    mediaSession: null,
    artworkUrl: (id) => id,
    userAgent: "test",
    maxTouchPoints: 0,
    now: Date.now,
    onPlayRecorded: () => undefined,
    ...options.deps,
  });
  // 命令只带曲目 ID：入队前把曲目交给替身，队列里才有完整的显示信息。
  for (const method of ["playTracks", "playNext", "enqueue"] as const) {
    const original = controller[method].bind(controller) as (tracks: readonly PlayerTrack[], startIndex?: number) => ReturnType<PlayerController["enqueue"]>;
    controller[method] = (tracks: readonly PlayerTrack[], startIndex?: number) => {
      server.register(tracks);
      return original(tracks, startIndex);
    };
  }
  /** 正在放当前流的元素。 */
  const playing = () => audios.find((audio) => audio.src !== "" && audio.src === server.zone().stream?.url);
  return { controller, audios, playing };
}

/** 一套出声的测试播放器：替身服务端、控制器与等待服务端响应的小工具。 */
export function playerHarness(options: { trackOf?: (trackId: string) => PlayerTrack } = {}) {
  const server = fakeZoneServer(options);
  const { controller, audios, playing } = testPlayerController(server, { audio: true });
  const queueIds = () => controller.getSnapshot().queue.entries.map((entry) => entry.track.trackId);
  return {
    server,
    engine: controller,
    audios,
    playing,
    queueIds,
    /** 等快照里的队列长度变成 length（命令的响应回来了）。 */
    queued: (length: number) => waitFor(() => expect(controller.getSnapshot().queue.entries).toHaveLength(length)),
    /** 当前流的元素开始出声，可选再报告一个流内位置。 */
    sounding: async (position?: number): Promise<FakeAudio> => {
      await waitFor(() => expect(playing()).toBeDefined());
      const audio = playing();
      if (audio === undefined) throw new Error("没有在放的元素");
      act(() => {
        audio.beginPlaying();
        if (position !== undefined) audio.tick(position);
      });
      await waitFor(() => expect(controller.getSnapshot().status).toBe("playing"));
      return audio;
    },
  };
}

/**
 * 整个应用渲染（renderApp）时默认装配用的是 zones/api 的 browserZoneApi：测试用 vi.mock 把它换成这个代理，
 * 每次调用转给 current() 此刻的替身服务端。
 */
export function delegatingZoneApi(current: () => FakeZoneServer): ZoneApi {
  return {
    fetchSnapshot: () => current().api.fetchSnapshot(),
    fetchZoneQueue: (zoneId) => current().api.fetchZoneQueue(zoneId),
    registerDevice: (body) => current().api.registerDevice(body),
    sendZoneCommand: (zoneId, command) => current().api.sendZoneCommand(zoneId, command),
    putDeviceVolume: (deviceId, volume, muted) => current().api.putDeviceVolume(deviceId, volume, muted),
    renameDevice: (deviceId, name) => current().api.renameDevice(deviceId, name),
    deleteDevice: (deviceId) => current().api.deleteDevice(deviceId),
    claimOutput: (deviceId, body) => current().api.claimOutput(deviceId, body),
    postOutputReport: (sessionId, body) => current().api.postOutputReport(sessionId, body),
    releaseOutput: (sessionId, body) => current().api.releaseOutput(sessionId, body),
    openEventStream: (sessionId, signal) => current().api.openEventStream(sessionId, signal),
  };
}
