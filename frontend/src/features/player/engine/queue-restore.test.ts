import { describe, expect, it, vi } from "vitest";
import { ApiError } from "../../../shared/api";
import { sampleBody } from "../../../test/api-samples";
import { deferred, type Deferred } from "../../../test/deferred";
import { decodeReleaseDetail } from "../../catalog/api/decoders";
import type { ReleaseDetailDTO } from "../../catalog";
import type { SavedQueue } from "./persistence";
import { loadSavedReleases, rebuildQueue, RESTORE_CONCURRENCY } from "./queue-restore";

const sample = decodeReleaseDetail(sampleBody("releases/get"));
const [first, second, third] = sample.media.flatMap((medium) => medium.tracks);
if (first === undefined || second === undefined || third === undefined) throw new Error("测试样本至少要有三首曲目");

const group = "00000000-0000-4000-8000-0000000000c1";
const otherGroup = "00000000-0000-4000-8000-0000000000c2";
const releaseB = "00000000-0000-4000-8000-0000000000b2";
const missingTrack = "00000000-0000-4000-8000-0000000000d1";

/** 样本发行换一个 ID：同一组曲目出现在两张发行里，用来区分按发行查找。 */
const releaseWithId = (id: string): ReleaseDetailDTO => ({ ...sample, id });

const entry = (entryId: string, trackId: string, releaseId = sample.id, groupId = group) => ({ entryId, trackId, releaseId, groupId });

const savedQueue = (overrides: Partial<SavedQueue>): SavedQueue => ({ entries: [], currentEntryId: null, mode: "sequential", positionSeconds: 0, history: [], ...overrides });

const httpError = (status: number, code: string) => new ApiError({ kind: "http", status, code, message: "后端文案", requestId: "req-1" });

/** 让已排队的 Promise 回调都执行完。 */
async function settle(): Promise<void> {
  for (let turn = 0; turn < 8; turn += 1) await Promise.resolve();
}

describe("按发行读取保存的队列", () => {
  it("同一发行只读一次，最多 4 个同时在途，读完一个才发下一个", async () => {
    const ids = Array.from({ length: 6 }, (_, index) => `00000000-0000-4000-8000-0000000001${String(index).padStart(2, "0")}`);
    const pending = new Map<string, Deferred<ReleaseDetailDTO>>();
    const loadRelease = vi.fn((releaseId: string) => {
      const load = deferred<ReleaseDetailDTO>();
      pending.set(releaseId, load);
      return load.promise;
    });
    const entries = ids.flatMap((releaseId, index) => [entry(`a${index}`, first.id, releaseId), entry(`b${index}`, second.id, releaseId)]);

    const result = loadSavedReleases(entries, loadRelease, () => false);
    await settle();
    expect(RESTORE_CONCURRENCY).toBe(4);
    expect(loadRelease.mock.calls.map(([releaseId]) => releaseId)).toEqual(ids.slice(0, 4));

    pending.get(ids[1] ?? "")?.resolve(releaseWithId(ids[1] ?? ""));
    await settle();
    expect(loadRelease.mock.calls.map(([releaseId]) => releaseId)).toEqual(ids.slice(0, 5));

    for (const releaseId of ids) pending.get(releaseId)?.resolve(releaseWithId(releaseId));
    await settle();
    for (const releaseId of ids) pending.get(releaseId)?.resolve(releaseWithId(releaseId));
    const { releases, transientFailure } = await result;
    expect(loadRelease).toHaveBeenCalledTimes(6);
    expect([...releases.keys()].sort()).toEqual([...ids].sort());
    expect(transientFailure).toBe(false);
  });

  it("没有条目时不发出读取", async () => {
    const loadRelease = vi.fn(async () => sample);
    expect(await loadSavedReleases([], loadRelease, () => false)).toEqual({ releases: new Map(), transientFailure: false });
    expect(loadRelease).not.toHaveBeenCalled();
  });

  it("404 只说明发行已不存在：不算暂时性失败，其余发行照常读取", async () => {
    const loadRelease = vi.fn(async (releaseId: string) => {
      if (releaseId === releaseB) throw httpError(404, "not_found");
      return sample;
    });
    const { releases, transientFailure } = await loadSavedReleases([entry("e1", first.id), entry("e2", first.id, releaseB)], loadRelease, () => false);
    expect([...releases.keys()]).toEqual([sample.id]);
    expect(transientFailure).toBe(false);
  });

  it.each([
    ["网络失败", new ApiError({ kind: "network", requestId: "req-1" })],
    ["403", httpError(403, "forbidden")],
    ["503", httpError(503, "database_unavailable")],
    ["无效响应", new ApiError({ kind: "invalid_response", status: 200, requestId: "req-1" })],
    ["不是接口错误", new Error("意外")],
  ])("%s是暂时性失败：不能据此丢弃保存的条目", async (_name, error) => {
    const loadRelease = vi.fn(async (releaseId: string) => {
      if (releaseId === releaseB) throw error;
      return sample;
    });
    const { releases, transientFailure } = await loadSavedReleases([entry("e1", first.id), entry("e2", first.id, releaseB)], loadRelease, () => false);
    expect([...releases.keys()]).toEqual([sample.id]);
    expect(transientFailure).toBe(true);
  });

  it("shouldStop 为真后不再发出新的读取，已在途的照常完成", async () => {
    const ids = Array.from({ length: 6 }, (_, index) => `00000000-0000-4000-8000-0000000002${String(index).padStart(2, "0")}`);
    let stopped = false;
    const loadRelease = vi.fn(async (releaseId: string) => {
      stopped = true;
      return releaseWithId(releaseId);
    });
    const { releases } = await loadSavedReleases(ids.map((releaseId, index) => entry(`e${index}`, first.id, releaseId)), loadRelease, () => stopped);
    // 第一个读取发出后就要求停止：其余三个并发工作者在发出之前检查，不再读取。
    expect(loadRelease).toHaveBeenCalledTimes(1);
    expect([...releases.keys()]).toEqual([ids[0]]);
  });
});

describe("用发行详情重建队列", () => {
  const releases = new Map([
    [sample.id, sample],
    [releaseB, releaseWithId(releaseB)],
  ]);

  it("条目 ID、组 ID 与顺序沿用保存的，曲目信息取自详情；当前条目保留时位置也保留", () => {
    const restored = rebuildQueue(
      savedQueue({ entries: [entry("e1", second.id), entry("e2", first.id, releaseB, otherGroup)], currentEntryId: "e2", positionSeconds: 42, history: ["e1"] }),
      releases,
    );
    expect(restored.dropped).toBe(0);
    expect(restored.positionSeconds).toBe(42);
    expect(restored.queue).toMatchObject({ currentEntryId: "e2", mode: "sequential", history: ["e1"], bag: [] });
    expect(restored.queue.entries.map((item) => [item.entryId, item.track.trackId, item.track.releaseId, item.track.groupId])).toEqual([
      ["e1", second.id, sample.id, group],
      ["e2", first.id, releaseB, otherGroup],
    ]);
    expect(restored.queue.entries[0]?.track).toMatchObject({ title: second.title, albumTitle: sample.title, durationSeconds: second.duration_seconds });
  });

  it("发行没读到或曲目已不在发行里的条目被丢弃并计数，历史只留保留下来的条目", () => {
    const restored = rebuildQueue(
      savedQueue({
        entries: [entry("e1", first.id), entry("e2", missingTrack), entry("e3", first.id, "00000000-0000-4000-8000-0000000000b9"), entry("e4", third.id)],
        currentEntryId: "e4",
        positionSeconds: 7,
        history: ["e2", "e1", "e3"],
      }),
      releases,
    );
    expect(restored.dropped).toBe(2);
    expect(restored.queue.entries.map((item) => item.entryId)).toEqual(["e1", "e4"]);
    expect(restored.queue.history).toEqual(["e1"]);
    expect(restored).toMatchObject({ positionSeconds: 7, queue: { currentEntryId: "e4" } });
  });

  it("当前条目被丢弃时取它之后第一个保留下来的条目，位置从头开始", () => {
    const restored = rebuildQueue(
      savedQueue({ entries: [entry("e1", first.id), entry("e2", missingTrack), entry("e3", missingTrack), entry("e4", third.id)], currentEntryId: "e2", positionSeconds: 30 }),
      releases,
    );
    expect(restored).toMatchObject({ positionSeconds: 0, dropped: 2, queue: { currentEntryId: "e4" } });
  });

  it("当前条目之后没有保留下来的条目时没有当前项，不回头取前面的", () => {
    const restored = rebuildQueue(savedQueue({ entries: [entry("e1", first.id), entry("e2", missingTrack)], currentEntryId: "e2", positionSeconds: 30 }), releases);
    expect(restored).toMatchObject({ positionSeconds: 0, dropped: 1, queue: { currentEntryId: null } });
    expect(restored.queue.entries.map((item) => item.entryId)).toEqual(["e1"]);
  });

  it("保存时没有当前项：仍没有，位置从头开始", () => {
    const restored = rebuildQueue(savedQueue({ entries: [entry("e1", first.id)], currentEntryId: null, positionSeconds: 30 }), releases);
    expect(restored).toMatchObject({ positionSeconds: 0, dropped: 0, queue: { currentEntryId: null } });
  });

  it("随机模式的袋子是历史与当前项以外的保留条目", () => {
    const restored = rebuildQueue(
      savedQueue({
        entries: [entry("e1", first.id), entry("e2", second.id), entry("e3", missingTrack), entry("e4", third.id), entry("e5", first.id, releaseB)],
        currentEntryId: "e2",
        mode: "shuffle",
        history: ["e4", "e3"],
      }),
      releases,
    );
    expect(restored.queue).toMatchObject({ mode: "shuffle", currentEntryId: "e2", history: ["e4"], bag: ["e1", "e5"] });
  });

  it("所有条目都没保留下来：队列为空", () => {
    const restored = rebuildQueue(savedQueue({ entries: [entry("e1", missingTrack)], currentEntryId: "e1", positionSeconds: 5 }), new Map());
    expect(restored).toEqual({ queue: { entries: [], currentEntryId: null, mode: "sequential", history: [], bag: [] }, positionSeconds: 0, dropped: 1 });
  });
});
