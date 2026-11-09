import { afterEach, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import startedSample from "../../../../../backend/internal/app/testdata/api-samples/playback-zones/events/zone-track-started.json";
import recordedSample from "../../../../../backend/internal/app/testdata/api-samples/playback-zones/events/zone-play-recorded.json";
import { fakeZoneServer, testPlayerController } from "../../../test/fake-zones";
import { playerTrack } from "../../../test/player-data";
import { memoryStorage } from "../../../test/memory-storage";
import { decodeStreamEvent } from "./decoders";
import { deviceStorageKey } from "./device";
import type { PlayerController } from "./controller";

const controllers: PlayerController[] = [];
afterEach(() => {
  for (const controller of controllers.splice(0)) controller.endSession();
  vi.useRealTimers();
});

it("控制端按服务端时钟偏移外推进度，且不超过曲目时长", async () => {
  vi.useFakeTimers();
  vi.setSystemTime(10_000);
  const server = fakeZoneServer();
  await server.api.sendZoneCommand(server.zone().zoneId, { command: "play_tracks", track_ids: ["track-一"], start_index: 0 });
  server.update({ status: "playing", position: { seconds: 5, at: 48_000, durationSeconds: 20, seekable: true } });
  const { controller } = testPlayerController(server, { deps: { api: {
    ...server.api,
    fetchSnapshot: async () => ({ serverTime: 50_000, devices: [server.device()], zones: [server.zone()] }),
  } } });
  controllers.push(controller);
  controller.attach();
  await vi.waitFor(() => expect(controller.getSnapshot().restoring).toBe(false));
  const initial = controller.getProgress().positionSeconds;
  // vi.waitFor 推进的本地时间也计入外推；服务器比本地快 40 秒。
  expect(initial).toBeCloseTo(7, 0);
  await vi.advanceTimersByTimeAsync(1_000);
  expect(controller.getProgress().positionSeconds).toBeCloseTo(initial + 1, 5);
  await vi.advanceTimersByTimeAsync(30_000);
  expect(controller.getProgress()).toEqual({ positionSeconds: 20, durationSeconds: 20, seekable: true });
});

it("首个快照中的 track_started 不再公告，随后 play_recorded 只通知一次", async () => {
  const server = fakeZoneServer();
  const startedEvent = decodeStreamEvent(startedSample.event, JSON.stringify(startedSample.data));
  const recordedEvent = decodeStreamEvent(recordedSample.event, JSON.stringify(recordedSample.data));
  if (startedEvent?.type !== "zone" || recordedEvent?.type !== "zone") throw new Error("样本应为播放区事件");
  const started = startedEvent.zone;
  const recorded = recordedEvent.zone;
  const device = { ...server.device(), deviceId: started.deviceId, zoneId: started.zoneId };
  const userId = "00000000-0000-4000-8000-000000000003";
  const onPlayRecorded = vi.fn();
  let push: (text: string) => void = () => { throw new Error("事件流尚未打开"); };
  const body = new ReadableStream<Uint8Array>({ start(sink) { push = (text) => sink.enqueue(new TextEncoder().encode(text)); } });
  const openEventStream = vi.fn(async () => new Response(body));
  const { controller } = testPlayerController(server, { deps: {
    onPlayRecorded,
    localStorage: memoryStorage({ [deviceStorageKey(userId)]: JSON.stringify({ version: 1, deviceId: device.deviceId }) }),
    api: {
      ...server.api,
      fetchSnapshot: async () => ({ serverTime: started.position.at, devices: [device], zones: [started] }),
      fetchZoneQueue: async () => ({ zoneId: started.zoneId, queueRevision: started.queueRevision, entries: started.current === null ? [] : [started.current] }),
      openEventStream,
    },
  } });
  controllers.push(controller);
  controller.attach();
  await waitFor(() => expect(controller.getSnapshot().restoring).toBe(false));
  expect(controller.getSnapshot().current?.entryId).toBe(started.current?.entryId);
  expect(controller.getSnapshot().event).toBeNull();
  expect(onPlayRecorded).not.toHaveBeenCalled();
  push(`event: ${recordedSample.event}\ndata: ${JSON.stringify(recordedSample.data)}\n\n`);
  await waitFor(() => expect(controller.getSnapshot().event).toEqual(recorded.event));
  expect(onPlayRecorded).toHaveBeenCalledTimes(1);
  // 新 revision 会被采用，但携带同一个事件 seq 时不应重复让历史失效。
  const newerZone = { ...recordedSample.data.zone, revision: recorded.revision + 1 };
  push(`event: ${recordedSample.event}\ndata: ${JSON.stringify({ zone: newerZone })}\n\n`);
  await waitFor(() => expect(controller.getSnapshot().zone?.revision).toBe(newerZone.revision));
  expect(controller.getSnapshot().event).toEqual(recorded.event);
  expect(onPlayRecorded).toHaveBeenCalledTimes(1);
});

it("重连后的快照说设备已不在本会话上：带正在放的条目与位置重新认领，不先卸载媒体", async () => {
  const server = fakeZoneServer();
  const pushes: ((text: string) => void)[] = [];
  const openEventStream = vi.fn(async (session: string | null) => {
    const body = new ReadableStream<Uint8Array>({ start(sink) { if (session !== null) pushes.push((text) => sink.enqueue(new TextEncoder().encode(text))); } });
    return new Response(body);
  });
  const { controller, playing } = testPlayerController(server, { audio: true, deps: { api: { ...server.api, openEventStream } } });
  controllers.push(controller);
  controller.attach();
  await waitFor(() => expect(server.claims).toHaveLength(1));
  controller.playTracks([playerTrack("一")]);
  await waitFor(() => expect(playing()).toBeDefined());
  const audio = playing();
  if (audio === undefined) throw new Error("没有在放的元素");
  audio.beginPlaying();
  audio.tick(12);
  await waitFor(() => expect(pushes.length).toBeGreaterThan(0));
  const entryId = controller.getSnapshot().current?.entryId;
  // 服务重启：带旧会话重连的事件流照常返回快照，但设备离线、没有输出会话。
  const device = server.device();
  const offline = {
    device_id: device.deviceId, zone_id: device.zoneId, name: device.name, kind: "browser", online: false, last_seen_at: "2026-01-01T00:00:00.000Z",
    volume: device.volume, muted: device.muted, capabilities: { volume: "number" }, output_session_id: null,
  };
  pushes.at(-1)?.(`event: snapshot\ndata: ${JSON.stringify({ server_time: "2026-01-01T00:00:00.000Z", protocol: 1, devices: [offline], zones: [] })}\n\n`);
  await waitFor(() => expect(server.claims).toHaveLength(2));
  expect(server.claims[1]).toMatchObject({ takeover: false, playing: { entry_id: entryId, position_seconds: 12 } });
  expect(audio.src).not.toBe("");
  expect(audio.paused).toBe(false);
});
