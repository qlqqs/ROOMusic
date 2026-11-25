import { afterEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import startedSample from "../../../../../backend/internal/app/testdata/api-samples/playback-zones/events/zone-track-started.json";
import recordedSample from "../../../../../backend/internal/app/testdata/api-samples/playback-zones/events/zone-play-recorded.json";
import { fakeZoneServer, testPlayerController } from "../../../test/fake-zones";
import { playerTrack } from "../../../test/player-data";
import { memoryStorage } from "../../../test/memory-storage";
import { deferred } from "../../../test/deferred";
import { groupOf, leaderOf, memberOf, slimDevice, slimZone } from "../../../test/group-data";
import { rejectionLabels } from "../components/player-labels";
import { decodeStreamEvent, type DeviceResult, type RegisterResult } from "./decoders";
import { deviceStorageKey } from "./device";
import type { PlayerController } from "./controller";
import type { Device, Zone } from "./model";

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

it("本机拖动后服务端版本已更新、输出端还在旧流上时，进度停在目标位置，新流出声后从那里走", async () => {
  const server = fakeZoneServer();
  const { controller, playing } = testPlayerController(server, { audio: true });
  controllers.push(controller);
  controller.attach();
  await waitFor(() => expect(server.claims).toHaveLength(1));
  controller.playTracks([playerTrack("一", { durationSeconds: 60 })]);
  await waitFor(() => expect(playing()).toBeDefined());
  const old = playing();
  if (old === undefined) throw new Error("没有在放的元素");
  old.beginPlaying();
  old.tick(10);
  await waitFor(() => expect(controller.getProgress().positionSeconds).toBe(10));
  const revision = controller.getSnapshot().zone?.revision;
  controller.seek(50);
  // 服务端确认定位（版本号变了、给出新流），本机仍在放旧流，旧播放头继续往前走。
  await waitFor(() => expect(controller.getSnapshot().zone?.revision).not.toBe(revision));
  old.tick(11);
  expect(controller.getProgress().positionSeconds).toBe(50);
  const next = playing();
  if (next === undefined || next === old) throw new Error("新流没有加载到备用元素");
  next.beginPlaying();
  next.tick(1);
  expect(controller.getProgress().positionSeconds).toBe(51);
});

describe("局域网播放设备", () => {
  const userId = "00000000-0000-4000-8000-000000000003";
  const otherId = "00000000-0000-4000-8000-000000000004";

  async function setup() {
    const server = fakeZoneServer();
    const ownOccupant = { userId, displayName: "自己" };
    const others = { userId: otherId, displayName: "书房成员" };
    const renderer = (suffix: string, name: string, occupiedBy: typeof others | null): Device => ({
      ...server.device(),
      deviceId: `00000000-0000-4000-8000-00000000d00${suffix}`,
      zoneId: `00000000-0000-4000-8000-00000000e00${suffix}`,
      name,
      kind: "upnp",
      online: true,
      lan: { maxSampleRate: 48000, formatDeclared: false, occupiedBy, unavailableReason: null },
    });
    const free = renderer("2", "空闲音响", null);
    const mine = renderer("3", "自己的音响", ownOccupant);
    const busy = renderer("4", "书房音响", others);
    const devices = [free, mine, busy];
    const zones = devices.map(({ zoneId, deviceId }) => ({ ...server.zone(), zoneId, deviceId }));
    const activation = deferred<DeviceResult>();
    const activate = vi.fn(() => activation.promise);
    const saveRate = vi.fn(async () => ({
      serverTime: Date.now(),
      device: { ...free, lan: { maxSampleRate: 96000, formatDeclared: false, occupiedBy: null, unavailableReason: null } },
    }));
    const { controller } = testPlayerController(server, { deps: { userId, api: {
      ...server.api,
      fetchSnapshot: async () => ({ serverTime: Date.now(), devices: [server.device(), ...devices], zones: [server.zone(), ...zones] }),
      activateDevice: activate,
      putDeviceSampleRate: saveRate,
    } } });
    controllers.push(controller);
    controller.attach();
    await waitFor(() => expect(controller.getSnapshot().devices).toHaveLength(4));
    return { controller, free, busy, ownOccupant, activation, activate, saveRate };
  }

  it("激活成功后应用返回设备并切换控制目标", async () => {
    const { controller, busy, ownOccupant, activation, activate } = await setup();
    const pending = controller.activateDevice(busy.deviceId, true);
    expect(activate).toHaveBeenCalledExactlyOnceWith(busy.deviceId, true);
    expect(controller.getSnapshot().isLocal).toBe(true);
    const device = {
      ...busy,
      lan: { maxSampleRate: 48000, formatDeclared: false, occupiedBy: ownOccupant, unavailableReason: null },
    };
    activation.resolve({ serverTime: Date.now(), device });
    await pending;
    expect(controller.getSnapshot()).toMatchObject({ device, isLocal: false, zone: { zoneId: busy.zoneId } });
    expect(controller.getSnapshot().devices.find((row) => row.device.deviceId === busy.deviceId)).toMatchObject({ isTarget: true, occupant: null });
  });

  it("采样率上限的响应写回对应设备", async () => {
    const { controller, free, saveRate } = await setup();
    await controller.setDeviceSampleRate(free.deviceId, 96000);
    expect(saveRate).toHaveBeenCalledExactlyOnceWith(free.deviceId, 96000);
    expect(controller.getSnapshot().devices.find((row) => row.device.deviceId === free.deviceId)?.device.lan)
      .toStrictEqual({ maxSampleRate: 96000, formatDeclared: false, occupiedBy: null, unavailableReason: null });
  });

  it("设备行只显示其他成员的占用者名称", async () => {
    const { controller } = await setup();
    expect(Object.fromEntries(controller.getSnapshot().devices.map((row) => [row.device.name, row.occupant]))).toEqual({
      测试设备: null,
      空闲音响: null,
      自己的音响: null,
      书房音响: "书房成员",
    });
  });
});

describe("SlimProto 编组", () => {
  /**
   * 正在控制“客厅”（主设备）：组里有“书房”（同步中）；“卧室”是可以加入的空闲设备。
   * 替身服务端只记下请求、按合同回写：组音量把最响的一台调到目标值，其余按原比例缩放。
   */
  async function setup(options: { leaderFixed?: boolean; memberFixed?: boolean } = {}) {
    const server = fakeZoneServer();
    const base = server.zone();
    const leaderZoneId = slimDevice(1, "客厅").zoneId;
    let leader = slimDevice(1, "客厅", { volume: 0.4, volumeControllable: options.leaderFixed !== true, group: { zoneId: leaderZoneId, role: "leader" } });
    let member = slimDevice(2, "书房", { volume: 0.8, volumeControllable: options.memberFixed !== true, group: { zoneId: leaderZoneId, role: "member" }, syncDelayMs: 0 });
    const candidate = slimDevice(3, "卧室");
    const leaderZone = slimZone(base, leader, { revision: 5, group: groupOf([member, "active"]) });
    const zones = [leaderZone, slimZone(base, member), slimZone(base, candidate)];
    const byId = () => new Map([leader, member, candidate].map((device) => [device.deviceId, device]));
    const putDeviceVolume = vi.fn(async (deviceId: string, volume: number, muted: boolean) => {
      const device = { ...byId().get(deviceId), volume, muted } as Device;
      return { serverTime: Date.now(), device };
    });
    const putGroupVolume = vi.fn(async (_zoneId: string, volume: number) => {
      const top = Math.max(leader.volume, member.volume);
      leader = { ...leader, volume: top === 0 ? volume : leader.volume * volume / top };
      member = { ...member, volume: top === 0 ? volume : member.volume * volume / top };
      return { serverTime: Date.now(), devices: [leader, member] };
    });
    const addGroupMember = vi.fn(async (zoneId: string, deviceId: string, _takeover: boolean): Promise<RegisterResult> => ({
      serverTime: Date.now(),
      device: { ...(byId().get(deviceId) ?? candidate), group: { zoneId, role: "member" } },
      zone: { ...leaderZone, revision: 6, group: groupOf([member, "active"], [deviceId, "joining"]) },
    }));
    const removeGroupMember = vi.fn(async (_zoneId: string, deviceId: string) => ({
      serverTime: Date.now(),
      device: { ...(byId().get(deviceId) ?? member), group: null },
      zone: { ...leaderZone, revision: 7, group: null },
    }));
    const putSyncDelay = vi.fn(async (deviceId: string, ms: number) => ({ serverTime: Date.now(), device: { ...(byId().get(deviceId) ?? member), syncDelayMs: ms } }));
    const { controller } = testPlayerController(server, { deps: { now: () => Date.now(), api: {
      ...server.api,
      fetchSnapshot: async () => ({ serverTime: Date.now(), devices: [server.device(), leader, member, candidate], zones: [server.zone(), ...zones] }),
      putDeviceVolume, putGroupVolume, addGroupMember, removeGroupMember, putSyncDelay,
    } } });
    controllers.push(controller);
    controller.attach();
    await vi.waitFor(() => expect(controller.getSnapshot().devices).toHaveLength(4));
    controller.setControlTarget(leaderZone.zoneId);
    expect(controller.getSnapshot().zone?.zoneId).toBe(leaderZone.zoneId);
    const row = (deviceId: string) => controller.getSnapshot().devices.find((item) => item.device.deviceId === deviceId);
    return { controller, leader, member, candidate, leaderZone, row, putDeviceVolume, putGroupVolume, addGroupMember, removeGroupMember, putSyncDelay };
  }

  it("加入成员：按参数发出请求，写回主设备播放区的成员与被加入设备的组身份", async () => {
    const { controller, candidate, leaderZone, member, row, addGroupMember } = await setup();
    await controller.addGroupMember(leaderZone.zoneId, candidate.deviceId, true);
    expect(addGroupMember).toHaveBeenCalledExactlyOnceWith(leaderZone.zoneId, candidate.deviceId, true);
    expect(controller.getSnapshot().zone?.group).toStrictEqual(groupOf([member, "active"], [candidate, "joining"]));
    expect(row(candidate.deviceId)?.device.group).toStrictEqual(memberOf(leaderZone));
  });

  it("加入成员的响应里是列表里看不到的设备时，不把它加进设备列表", async () => {
    const { controller, leaderZone, addGroupMember } = await setup();
    const hidden = slimDevice(9, "看不到的播放器");
    addGroupMember.mockResolvedValueOnce({ serverTime: Date.now(), device: { ...hidden, group: memberOf(leaderZone) }, zone: { ...leaderZone, revision: 6, group: groupOf([hidden, "waiting"]) } });
    await controller.addGroupMember(leaderZone.zoneId, hidden.deviceId, false);
    expect(controller.getSnapshot().devices.map((item) => item.device.deviceId)).not.toContain(hidden.deviceId);
    expect(controller.getSnapshot().zone?.group).toStrictEqual(groupOf([hidden, "waiting"]));
  });

  it("移出成员：写回没有组的播放区，成员设备不再带组身份", async () => {
    const { controller, member, leaderZone, row, removeGroupMember } = await setup();
    await controller.removeGroupMember(leaderZone.zoneId, member.deviceId);
    expect(removeGroupMember).toHaveBeenCalledExactlyOnceWith(leaderZone.zoneId, member.deviceId);
    expect(controller.getSnapshot().zone?.group).toBeNull();
    expect(row(member.deviceId)?.device.group).toBeNull();
  });

  it("同步微调：发出毫秒值并写回设备", async () => {
    const { controller, member, row, putSyncDelay } = await setup();
    await controller.setSyncDelay(member.deviceId, -120);
    expect(putSyncDelay).toHaveBeenCalledExactlyOnceWith(member.deviceId, -120);
    expect(row(member.deviceId)?.device.syncDelayMs).toBe(-120);
  });

  it("单独调组内一台：只发这台的设备音量、保留它的静音，本地值立即可见", async () => {
    const { controller, member, putDeviceVolume, putGroupVolume } = await setup();
    controller.setDeviceVolume(member.deviceId, 0.3);
    expect(putDeviceVolume).toHaveBeenCalledExactlyOnceWith(member.deviceId, 0.3, false);
    expect(putGroupVolume).not.toHaveBeenCalled();
    expect(controller.volumeOf(member)).toStrictEqual({ volume: 0.3, muted: false });
    // 组的显示值跟着变成可调设备里最响的那台（客厅 0.4）。
    expect(controller.getSnapshot().volume).toBe(0.4);
  });

  it("有组时音量控件显示可调设备里最响的一台，拖动走组音量并按节奏发送，本地值到期后回到服务端的值", async () => {
    const { controller, leaderZone, putDeviceVolume, putGroupVolume } = await setup();
    expect(controller.getSnapshot()).toMatchObject({ volume: 0.8, muted: false, volumeControllable: true });
    vi.useFakeTimers();
    controller.setVolume(0.3);
    controller.setVolume(0.4);
    controller.setVolume(0.6);
    expect(putGroupVolume.mock.calls).toEqual([[leaderZone.zoneId, 0.3]]);
    expect(controller.getSnapshot().volume).toBe(0.6);
    await vi.advanceTimersByTimeAsync(124);
    expect(putGroupVolume).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(putGroupVolume.mock.calls).toEqual([[leaderZone.zoneId, 0.3], [leaderZone.zoneId, 0.6]]);
    // 越界值收进 0…1；节流窗口过后再拖动立即发出。
    await vi.advanceTimersByTimeAsync(200);
    controller.setVolume(1.5);
    expect(putGroupVolume.mock.calls.at(-1)).toEqual([leaderZone.zoneId, 1]);
    expect(putDeviceVolume).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1_300);
    const devices = controller.getSnapshot().devices.filter((item) => item.device.group !== null).map((item) => item.device.volume);
    expect(controller.getSnapshot().volume).toBeCloseTo(Math.max(...devices), 10);
    expect(controller.getSnapshot().volume).toBeCloseTo(1, 10);
  });

  it("组内有固定音量的设备时只按可调的那台显示，静音只发给可调的设备", async () => {
    const { controller, leader, member, putDeviceVolume } = await setup({ memberFixed: true });
    expect(controller.getSnapshot()).toMatchObject({ volume: leader.volume, volumeControllable: true });
    controller.setMuted(true);
    expect(putDeviceVolume.mock.calls).toEqual([[leader.deviceId, 0.4, true]]);
    expect(controller.getSnapshot().muted).toBe(true);
    expect(controller.volumeOf(member)).toStrictEqual({ volume: member.volume, muted: false });
  });

  it("组静音逐台静音，每台保留自己的音量；全部静音时控件显示静音，取消静音同样逐台", async () => {
    const { controller, leader, member, putDeviceVolume } = await setup();
    controller.setMuted(true);
    expect(putDeviceVolume.mock.calls).toEqual([[leader.deviceId, 0.4, true], [member.deviceId, 0.8, true]]);
    expect(controller.getSnapshot()).toMatchObject({ muted: true, volume: 0.8 });
    controller.setMuted(false);
    expect(controller.volumeOf(leader)).toStrictEqual({ volume: 0.4, muted: false });
    expect(controller.volumeOf(member)).toStrictEqual({ volume: 0.8, muted: false });
    expect(controller.getSnapshot().muted).toBe(false);
  });

  it("组内全部固定音量时音量控件禁用，拖动不发请求", async () => {
    const { controller, putDeviceVolume, putGroupVolume } = await setup({ leaderFixed: true, memberFixed: true });
    expect(controller.getSnapshot().volumeControllable).toBe(false);
    controller.setVolume(0.2);
    controller.setMuted(true);
    expect(putGroupVolume).not.toHaveBeenCalled();
    expect(putDeviceVolume).not.toHaveBeenCalled();
  });

  it("组与设备音量各自节流：拖组音量不挡住单台音量的第一次发送", async () => {
    const { controller, member, leaderZone, putDeviceVolume, putGroupVolume } = await setup();
    vi.useFakeTimers();
    controller.setGroupVolume(leaderZone.zoneId, 0.5);
    controller.setDeviceVolume(member.deviceId, 0.2);
    expect(putGroupVolume).toHaveBeenCalledExactlyOnceWith(leaderZone.zoneId, 0.5);
    expect(putDeviceVolume).toHaveBeenCalledExactlyOnceWith(member.deviceId, 0.2, false);
  });
});

it("起播类命令被拒为 device_grouped 时记成被拒命令，提示先移出组", async () => {
  const server = fakeZoneServer();
  const { controller } = testPlayerController(server);
  controllers.push(controller);
  controller.attach();
  await waitFor(() => expect(controller.getSnapshot().restoring).toBe(false));
  server.failNext(409, "device_grouped");
  controller.playTracks([playerTrack("一")]);
  await waitFor(() => expect(controller.getSnapshot().rejection).toMatchObject({ reason: "device_grouped" }));
  const rejection = controller.getSnapshot().rejection;
  if (rejection === null) throw new Error("应记下被拒的命令");
  expect(rejectionLabels[rejection.reason]).toBe("这台设备在组里，先移出组。");
  // 下一条命令成功后提示清掉。
  controller.playTracks([playerTrack("二")]);
  await waitFor(() => expect(controller.getSnapshot().rejection).toBeNull());
});

it("设备与组字段：快照里的主设备播放区带组，控制成员自己的播放区时没有组", async () => {
  const server = fakeZoneServer();
  const leader = slimDevice(1, "客厅");
  const leaderZone: Zone = slimZone(server.zone(), leader, { group: groupOf([slimDevice(2, "书房"), "waiting"]) });
  const member = slimDevice(2, "书房", { group: memberOf(leaderZone) });
  const { controller } = testPlayerController(server, { deps: { api: {
    ...server.api,
    fetchSnapshot: async () => ({ serverTime: Date.now(), devices: [server.device(), { ...leader, group: leaderOf(leaderZone) }, member], zones: [server.zone(), leaderZone, slimZone(server.zone(), member)] }),
  } } });
  controllers.push(controller);
  controller.attach();
  await waitFor(() => expect(controller.getSnapshot().devices).toHaveLength(3));
  controller.setControlTarget(member.zoneId);
  expect(controller.getSnapshot()).toMatchObject({ zone: { zoneId: member.zoneId, group: null }, volume: member.volume, volumeControllable: true });
  controller.setControlTarget(leaderZone.zoneId);
  expect(controller.getSnapshot().zone?.group).toStrictEqual(groupOf([member, "waiting"]));
});

describe("第三方客户端的设备", () => {
  it("不在可选的播放设备里，单列在 clients；改它的设置发给它的播放区，不动控制目标", async () => {
    const server = fakeZoneServer();
    const client: Device = { ...server.device(), deviceId: "00000000-0000-4000-8000-00000000d0c1", zoneId: "00000000-0000-4000-8000-00000000e0c1", name: "Symfonium", kind: "subsonic" };
    const clientZone: Zone = { ...server.zone(), zoneId: client.zoneId, deviceId: client.deviceId };
    const sent: { zoneId: string; command: string }[] = [];
    const { controller } = testPlayerController(server, { deps: { api: {
      ...server.api,
      fetchSnapshot: async () => ({ serverTime: Date.now(), devices: [server.device(), client], zones: [server.zone(), clientZone] }),
      sendZoneCommand: async (zoneId, command) => {
        sent.push({ zoneId, command: command.command });
        return { serverTime: Date.now(), zone: clientZone, queueChange: null, targetZone: null, imported: null };
      },
    } } });
    controllers.push(controller);
    controller.attach();
    await waitFor(() => expect(controller.getSnapshot().clients.map((row) => row.device.name)).toEqual(["Symfonium"]));
    expect(controller.getSnapshot().devices.map((row) => row.device.kind)).toEqual(["browser"]);
    controller.setZoneSettings(client.zoneId, { leveling: "off" });
    await waitFor(() => expect(sent).toEqual([{ zoneId: client.zoneId, command: "set_settings" }]));
    expect(controller.getSnapshot().zone?.zoneId).toBe(server.zone().zoneId);
  });
});
