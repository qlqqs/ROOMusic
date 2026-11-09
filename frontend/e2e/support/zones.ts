import { readFileSync } from "node:fs";
import type { Page } from "@playwright/test";
import type { TrackSummaryDTO } from "../../src/features/catalog/api/track-summary";
import type { Zone } from "../../src/features/player/zones/model";
import * as d from "../../src/shared/api/decode";
import type { ApiMockRoute } from "./api-mock";

const BASE = "/api/v1/me/playback";
const DEVICE = "00000000-0000-4000-8000-00000000d001";
export const MOCK_ZONE = "00000000-0000-4000-8000-00000000e001";
const SESSION = "00000000-0000-4000-8000-00000000f001";
type Entry = { entry_id: string; track: TrackSummaryDTO };
type Stream = {
  seq: number; url: string; mime_type: string; quality: "lossless";
  format: { codec: string; sample_rate: number; bit_depth: number; channels: number };
  segments: { entry_id: string; track: TrackSummaryDTO; stream_start_seconds: number; track_start_seconds: number }[];
};
const now = () => new Date().toISOString();

/** 只模拟现有界面用到的单播放区合同；音频仍由原生 Audio 解码，不模拟浏览器媒体事件或计次。 */
export function zonesMock(trackOf: (id: string) => TrackSummaryDTO | undefined) {
  let registered = false;
  let counter = 0;
  let entries: Entry[] = [];
  const known = new Map<string, TrackSummaryDTO>();
  const device = {
    device_id: DEVICE, zone_id: MOCK_ZONE, name: "测试浏览器", kind: "browser", online: false, last_seen_at: now(),
    volume: 1, muted: false, output_session_id: null as string | null,
    capabilities: { stream_formats: [], max_channels: 2, dsd: "none", exclusive: false, volume: "number" },
  };
  const zone = {
    zone_id: MOCK_ZONE, device_id: DEVICE, revision: 1, queue_revision: 0, queue_length: 0,
    mode: "sequential" as Zone["mode"], intent: "pause" as Zone["intent"], status: "idle" as Zone["status"],
    settings: { leveling: "auto", gapless: true, crossfade: false, crossfade_seconds: 5, quality: "lossless", headroom: { enabled: false, db: 0, clip_indicator: false } },
    current: null as Entry | null, has_next: false, fault: null, failures: [],
    position: { seconds: 0, at: now(), duration_seconds: null as number | null, seekable: false },
    stream: null as Stream | null, next_stream: null, paused_at: null as string | null, signal: null,
    event: null as { seq: number; kind: "track_started"; entry_id: string } | null,
  };
  const snapshot = () => ({ server_time: now(), protocol: 1, devices: registered ? [device] : [], zones: registered ? [zone] : [] });
  const queue = () => ({ zone_id: MOCK_ZONE, queue_revision: zone.queue_revision, entries });
  const currentIndex = () => entries.findIndex((entry) => entry.entry_id === zone.current?.entry_id);
  const update = () => {
    zone.revision += 1;
    zone.queue_length = entries.length;
    zone.has_next = entries.length > 0 && (zone.mode === "repeat_all" || zone.mode === "shuffle" || currentIndex() < entries.length - 1);
  };
  const setCurrent = (entry: Entry | null, seconds = 0) => {
    zone.current = entry;
    zone.position = { seconds, at: now(), duration_seconds: entry?.track.duration_seconds ?? null, seekable: entry?.track.duration_seconds != null };
  };
  const open = (entry: Entry, seconds = 0) => {
    setCurrent(entry, seconds);
    const seq = ++counter;
    zone.stream = {
      seq, url: `${BASE}/zones/${MOCK_ZONE}/streams/${seq}`, mime_type: "audio/flac", quality: "lossless",
      format: { codec: "flac", sample_rate: 44100, bit_depth: 16, channels: 2 },
      segments: [{ entry_id: entry.entry_id, track: entry.track, stream_start_seconds: 0, track_start_seconds: seconds }],
    };
    zone.status = zone.intent === "play" ? "loading" : "paused";
  };
  const start = (entry: Entry) => {
    zone.intent = "play";
    zone.paused_at = null;
    open(entry);
    zone.event = { seq: zone.revision + 1, kind: "track_started", entry_id: entry.entry_id };
  };
  const newEntries = (ids: string[]) => ids.map((id): Entry => {
    const track = known.get(id) ?? trackOf(id);
    if (track === undefined) throw new Error(`播放区夹具没有登记曲目 ${id}`);
    return { entry_id: `00000000-0000-4000-8000-${String(++counter).padStart(12, "0")}`, track };
  });
  const command = (input: unknown) => {
    const { command: name } = d.object({ command: d.string() })(input, "$");
    let changed = false;
    switch (name) {
      case "play_tracks": {
        const body = d.object({ track_ids: d.array(d.uuid(), { max: 10_000 }), start_index: d.integer() })(input, "$");
        entries = newEntries(body.track_ids);
        const first = entries[body.start_index] ?? entries[0];
        if (first) start(first);
        changed = true;
        break;
      }
      case "play_next":
      case "enqueue": {
        const body = d.object({ track_ids: d.array(d.uuid(), { max: 10_000 }) })(input, "$");
        const added = newEntries(body.track_ids);
        entries.splice(name === "enqueue" ? entries.length : currentIndex() + 1, 0, ...added);
        if (zone.current === null) { setCurrent(entries[0] ?? null); zone.status = "paused"; }
        changed = true;
        break;
      }
      case "pause":
        zone.intent = "pause";
        zone.status = "paused";
        zone.paused_at = now();
        break;
      case "play":
        zone.intent = "play";
        if (zone.current && zone.stream === null) open(zone.current, zone.position.seconds);
        break;
      case "next":
      case "previous": {
        const entry = entries[currentIndex() + (name === "next" ? 1 : -1)];
        if (entry) start(entry);
        break;
      }
      case "seek": {
        const body = d.object({ position_seconds: d.number({ min: 0 }) })(input, "$");
        if (zone.current) open(zone.current, Math.min(body.position_seconds, zone.position.duration_seconds ?? Infinity));
        break;
      }
      case "play_entry": {
        const body = d.object({ entry_id: d.uuid() })(input, "$");
        const entry = entries.find((item) => item.entry_id === body.entry_id);
        if (entry) start(entry);
        break;
      }
      case "move_entry": {
        const body = d.object({ entry_id: d.uuid(), delta: d.integer({ min: -1, max: 1 }) })(input, "$");
        const from = entries.findIndex((item) => item.entry_id === body.entry_id);
        const entry = entries[from];
        if (entry && entries[from + body.delta]) { entries.splice(from, 1); entries.splice(from + body.delta, 0, entry); changed = true; }
        break;
      }
      case "remove_entry": {
        const body = d.object({ entry_id: d.uuid() })(input, "$");
        const from = entries.findIndex((item) => item.entry_id === body.entry_id);
        if (from < 0) break;
        entries.splice(from, 1);
        if (zone.current?.entry_id === body.entry_id) {
          const entry = entries[from] ?? null;
          setCurrent(entry);
          zone.stream = null;
          if (entry && zone.intent === "play") start(entry);
          else zone.status = entry ? "paused" : "idle";
        }
        changed = true;
        break;
      }
      case "clear_queue":
        entries = [];
        setCurrent(null);
        zone.stream = null;
        zone.intent = "pause";
        zone.status = "idle";
        changed = true;
        break;
      default: throw new Error(`冒烟夹具尚未实现命令 ${name}`);
    }
    if (changed) zone.queue_revision += 1;
    update();
    return { server_time: now(), zone, queue_change: changed ? { kind: "replace", queue_revision: zone.queue_revision, entries } : null };
  };
  return {
    register(tracks: readonly TrackSummaryDTO[]) { for (const track of tracks) known.set(track.track_id, track); },
    queueSnapshot: () => structuredClone(queue()),
    currentTrack: () => zone.current?.track.track_id,
    streamUrl: () => zone.stream?.url,
    async handle(route: ApiMockRoute, path: string, method: string, input: unknown): Promise<void> {
      const json = (body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
      if (path === BASE) return json(snapshot());
      if (path === `${BASE}/events`) {
        // Playwright 的 fulfill 一次交付有限响应；每次重连仍给完整快照，后续变化由命令/回报响应传回。
        return route.fulfill({ status: 200, contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(snapshot())}\n\n` });
      }
      if (path === `${BASE}/devices` && method === "POST") {
        registered = true;
        return json({ server_time: now(), device, zone }, 201);
      }
      if (path.endsWith("/output") && method === "POST") {
        device.online = true;
        device.output_session_id = SESSION;
        return json({ server_time: now(), protocol: 1, session_id: SESSION, device, zone });
      }
      if (path.endsWith("/release")) return route.fulfill({ status: 204 });
      if (path.endsWith("/queue")) return json(queue());
      if (path.endsWith("/commands")) return json(command(input));
      if (path.endsWith("/reports")) {
        const body = d.object({ stream_seq: d.nullable(d.integer()), stream_seconds: d.number(), status: d.oneOf(["loading", "playing", "paused", "buffering"] as const) })(input, "$");
        if (body.stream_seq === zone.stream?.seq) {
          zone.status = body.status;
          zone.position.seconds = (zone.stream?.segments[0]?.track_start_seconds ?? 0) + body.stream_seconds;
          zone.position.at = now();
          update();
        }
        return json({ server_time: now(), device, zone });
      }
      throw new Error(`冒烟夹具尚未实现 ${method} ${path}`);
    },
  };
}

/** 渲染流用仓库中的合成 FLAC；不再请求曲目预检或原文件直出地址。 */
export async function installZoneAudio(page: Page, audio = readFileSync(new URL("./audio/keyboard-60s.flac", import.meta.url))) {
  await page.route("**/api/v1/me/playback/zones/*/streams/*", async (route) => {
    const range = /^bytes=(\d+)-(\d*)$/.exec(route.request().headers()["range"] ?? "");
    const start = Number(range?.[1] ?? 0);
    const end = Math.min(Number(range?.[2] || audio.length - 1), audio.length - 1);
    if (start > end) return route.fulfill({ status: 416, headers: { "Content-Range": `bytes */${audio.length}` } });
    await route.fulfill({ status: range ? 206 : 200, contentType: "audio/flac", body: audio.subarray(start, end + 1), headers: {
      "Accept-Ranges": "bytes", "Content-Length": String(end - start + 1),
      ...(range ? { "Content-Range": `bytes ${start}-${end}/${audio.length}` } : {}),
    } });
  });
}
