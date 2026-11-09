import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { fakeZoneServer, testPlayerController } from "../../../test/fake-zones";
import { memoryStorage } from "../../../test/memory-storage";
import { CONTROL_TARGET_IDLE_MS, controlTargetStorageKey, readControlTarget } from "./control-target";
import type { PlayerController } from "./controller";
import { deviceStorageKey } from "./device";

const USER = "00000000-0000-4000-8000-000000000003";
const REMOTE_ZONE = "00000000-0000-4000-8000-00000000e002";
const REMOTE_DEVICE = "00000000-0000-4000-8000-00000000d002";
const NOW = 10_000_000;
const SERVER_OFFSET = 120_000;
const controllers: PlayerController[] = [];

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(NOW);
});
afterEach(() => {
  for (const controller of controllers.splice(0)) controller.endSession();
  vi.useRealTimers();
});

async function harness(options: { online?: boolean; missing?: boolean; pausedFor?: number; selectedAt?: number } = {}) {
  const server = fakeZoneServer();
  const remoteDevice = { ...server.device(), deviceId: REMOTE_DEVICE, zoneId: REMOTE_ZONE, online: options.online ?? true };
  const remoteZone = { ...server.zone(), zoneId: REMOTE_ZONE, deviceId: REMOTE_DEVICE,
    pausedAt: NOW + SERVER_OFFSET - (options.pausedFor ?? 0) };
  const storage = memoryStorage({
    [deviceStorageKey(USER)]: JSON.stringify({ version: 1, deviceId: server.device().deviceId }),
    ...(options.selectedAt === undefined ? {} : {
      [controlTargetStorageKey(USER)]: JSON.stringify({ version: 1, zoneId: REMOTE_ZONE, selectedAt: options.selectedAt }),
    }),
  });
  const { controller } = testPlayerController(server, { deps: { localStorage: storage, api: {
    ...server.api,
    fetchSnapshot: async () => ({ serverTime: Date.now() + SERVER_OFFSET,
      devices: [server.device(), remoteDevice], zones: options.missing ? [server.zone()] : [server.zone(), remoteZone] }),
    fetchZoneQueue: async (zoneId) => ({ zoneId, queueRevision: 0, entries: [] }),
  } } });
  controllers.push(controller);
  controller.attach();
  await vi.waitFor(() => expect(controller.getSnapshot().restoring).toBe(false));
  return { controller, storage, localZoneId: server.zone().zoneId };
}

it.each([
  { name: "目标离线", online: false },
  { name: "目标不存在", missing: true },
])("$name 时回退本机并清掉存储", async (options) => {
  const { controller, storage, localZoneId } = await harness({ ...options, selectedAt: NOW });
  expect(controller.getSnapshot()).toMatchObject({ isLocal: true, zone: { zoneId: localZoneId } });
  expect(storage.getItem(controlTargetStorageKey(USER))).toBeNull();
});

it("暂停超过 15 分钟后在每分钟检查时回退，采用服务端时钟偏移", async () => {
  const { controller, storage, localZoneId } = await harness({ pausedFor: CONTROL_TARGET_IDLE_MS - 30_000, selectedAt: NOW - CONTROL_TARGET_IDLE_MS });
  expect(controller.getSnapshot().zone?.zoneId).toBe(REMOTE_ZONE);
  await vi.advanceTimersByTimeAsync(60_000);
  expect(controller.getSnapshot()).toMatchObject({ isLocal: true, zone: { zoneId: localZoneId } });
  expect(storage.getItem(controlTargetStorageKey(USER))).toBeNull();
});

it("选中早已暂停的设备，从 selectedAt 起算 15 分钟，不立即回退", async () => {
  const { controller, storage, localZoneId } = await harness({ pausedFor: 60 * 60_000 });
  const selectedAt = Date.now();
  controller.setControlTarget(REMOTE_ZONE);
  expect(controller.getSnapshot().zone?.zoneId).toBe(REMOTE_ZONE);
  expect(readControlTarget(storage, USER)).toEqual({ zoneId: REMOTE_ZONE, selectedAt });
  await vi.advanceTimersByTimeAsync(CONTROL_TARGET_IDLE_MS);
  expect(controller.getSnapshot().zone?.zoneId).toBe(REMOTE_ZONE);
  await vi.advanceTimersByTimeAsync(60_000);
  expect(controller.getSnapshot()).toMatchObject({ isLocal: true, zone: { zoneId: localZoneId } });
  expect(storage.getItem(controlTargetStorageKey(USER))).toBeNull();
});

it("storage 事件同步其他标签页选择的目标及清除操作，保留 selectedAt", async () => {
  const { controller, storage, localZoneId } = await harness({ pausedFor: 60 * 60_000 });
  const key = controlTargetStorageKey(USER);
  const selectedAt = Date.now();
  const newValue = JSON.stringify({ version: 1, zoneId: REMOTE_ZONE, selectedAt });
  storage.setItem(key, newValue);
  expect(controller.getSnapshot().zone?.zoneId).toBe(localZoneId);
  window.dispatchEvent(new StorageEvent("storage", { key, oldValue: null, newValue }));
  expect(controller.getSnapshot().zone?.zoneId).toBe(REMOTE_ZONE);
  expect(readControlTarget(storage, USER)).toEqual({ zoneId: REMOTE_ZONE, selectedAt });
  storage.removeItem(key);
  window.dispatchEvent(new StorageEvent("storage", { key, oldValue: newValue, newValue: null }));
  expect(controller.getSnapshot()).toMatchObject({ isLocal: true, zone: { zoneId: localZoneId } });
});
