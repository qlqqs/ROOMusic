import { act, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CatalogStateDTO } from "../features/catalog/api/decoders";
import { decodeArtistQueue } from "../features/artists/api/decoders";
import type { TrackSummaryDTO } from "../features/catalog/api/track-summary";
import type { PlayerController } from "../features/player";
import { sampleBody } from "../test/api-samples";
import { deferred } from "../test/deferred";
import { playerHarness } from "../test/fake-zones";
import { jsonResponse, stubFetch } from "../test/http";
import { playerTrack } from "../test/player-data";
import { PlayerMetadataRefresh, usePlayerMetadataRefresh } from "./player-metadata-refresh";

const context = vi.hoisted(() => ({
  state: undefined as CatalogStateDTO | undefined,
  engine: undefined as PlayerController | undefined,
  scope: { isCurrent: (): boolean => true },
}));
vi.mock("../features/catalog", async (original) => ({
  ...await original<typeof import("../features/catalog")>(),
  useCatalogState: () => ({ data: context.state }),
}));
vi.mock("../features/auth", async (original) => ({
  ...await original<typeof import("../features/auth")>(), useViewerScope: () => context.scope,
}));
vi.mock("../features/player", async (original) => ({
  ...await original<typeof import("../features/player")>(), usePlayerEngine: () => context.engine,
}));

const id = (n: number) => `a0000000-0000-4000-8000-${n.toString(16).padStart(12, "0")}`;
const ready = (epoch = 2): CatalogStateDTO => ({ artists_state: "ready", search_mode: "normalized", epoch });
function RefreshButton() {
  const refresh = usePlayerMetadataRefresh();
  return <button onClick={refresh}>刷新队列显示</button>;
}
function mount() {
  return render(<PlayerMetadataRefresh><RefreshButton /></PlayerMetadataRefresh>);
}
function refresh() { act(() => screen.getByRole("button", { name: "刷新队列显示" }).click()); }
async function settle() { await act(async () => { for (let i = 0; i < 12; i += 1) await Promise.resolve(); }); }
function summary(trackId: string, title = "更新后的标题"): TrackSummaryDTO {
  const first = decodeArtistQueue(sampleBody("artists/queue")).items[0];
  if (first === undefined) throw new Error("真实艺人队列样本必须非空");
  return { ...first, track_id: trackId, title, artist: "新署名",
    artist_refs: [{ artist_id: id(90), name: "新署名", display_name: "新主名" }] };
}

function harness() {
  const player = playerHarness();
  player.engine.attach();
  context.engine = player.engine;
  return player;
}
let h: ReturnType<typeof harness>;
beforeEach(() => { context.state = ready(); context.scope = { isCurrent: (): boolean => true }; h = harness(); });
afterEach(() => { h.engine.endSession(); });

describe("I4 队列元数据刷新", () => {
  it("第一次读到 ready 不请求；preparing→ready 才刷新，未就绪及同 epoch 不重复刷新", async () => {
    h.engine.enqueue([playerTrack("一", { trackId: id(1) })]);
    await h.queued(1);
    const requests = stubFetch(() => jsonResponse(200, { items: [] }));
    context.state = undefined;
    const view = mount();
    context.state = ready(); view.rerender(<PlayerMetadataRefresh><RefreshButton /></PlayerMetadataRefresh>);
    await settle(); expect(requests).toHaveLength(0);
    context.state = { ...ready(), artists_state: "preparing" }; view.rerender(<PlayerMetadataRefresh><RefreshButton /></PlayerMetadataRefresh>);
    await settle(); expect(requests).toHaveLength(0);
    context.state = ready(3); view.rerender(<PlayerMetadataRefresh><RefreshButton /></PlayerMetadataRefresh>);
    await waitFor(() => expect(requests).toHaveLength(1));
    context.state = ready(3); view.rerender(<PlayerMetadataRefresh><RefreshButton /></PlayerMetadataRefresh>);
    await settle(); expect(requests).toHaveLength(1);
    context.state = ready(4); view.rerender(<PlayerMetadataRefresh><RefreshButton /></PlayerMetadataRefresh>);
    await waitFor(() => expect(requests).toHaveLength(2));
  });

  it("一次 POST 最多 1000 个去重 ID，从当前向后再接前面，不逐发行读取", async () => {
    const tracks = Array.from({ length: 1005 }, (_, index) => playerTrack(String(index), { trackId: id(index + 1) }));
    h.engine.playTracks([...tracks, tracks[0] ?? playerTrack("不可达")], 998);
    await h.queued(1006);
    const requests = stubFetch(() => jsonResponse(200, { items: [] }));
    mount(); refresh();
    await waitFor(() => expect(requests).toHaveLength(1));
    expect(requests[0]?.method).toBe("POST");
    expect(requests[0]?.path).toBe("/api/v1/tracks/summaries");
    const expected = [...tracks.slice(998), ...tracks.slice(0, 998)].map((track) => track.trackId).slice(0, 1000);
    expect(requests[0]?.body).toEqual({ track_ids: expected });
    expect(new Set(expected).size).toBe(1000);
  });

  it.each(["playing", "paused"] as const)("%s：仅更新展示，条目 ID/当前项/进度/流/播放状态不变，不发任何播放命令", async (status) => {
    h.engine.playTracks([playerTrack("一", { trackId: id(1) }), playerTrack("二", { trackId: id(2) }), playerTrack("一重复", { trackId: id(1) })], 1);
    await h.queued(3);
    const audio = await h.sounding(37);
    if (status === "paused") {
      h.engine.pause();
      await waitFor(() => expect(h.engine.getSnapshot().status).toBe("paused"));
    }
    const before = h.engine.getSnapshot(); const progress = h.engine.getProgress();
    const audioBefore = { sources: [...audio.sources], load: audio.loadCalls, play: audio.playCalls, pause: audio.pauseCalls };
    const commands = h.server.commands.length;
    const requests = stubFetch(() => jsonResponse(200, { items: [summary(id(1)), summary(id(2))] }));
    mount(); refresh();
    await waitFor(() => expect(h.engine.getSnapshot().queue.entries[0]?.track.title).toBe("更新后的标题"));
    const after = h.engine.getSnapshot();
    expect(requests).toHaveLength(1);
    expect(after.queue.entries.map((entry) => entry.entryId)).toEqual(before.queue.entries.map((entry) => entry.entryId));
    expect({ ...after.queue, entries: [] }).toEqual({ ...before.queue, entries: [] });
    expect(after.current?.entryId).toBe(before.current?.entryId);
    expect(after.current?.track.title).toBe("更新后的标题");
    expect(after.zone?.stream?.seq).toBe(before.zone?.stream?.seq);
    expect(after.zone?.stream?.segments[0]?.track.title).toBe("更新后的标题");
    expect(after.status).toBe(status); expect(h.engine.getProgress()).toEqual(progress);
    expect(after.queue.entries.map((entry) => entry.track.artistRefs)).toEqual(Array(3).fill(summary(id(1)).artist_refs));
    expect(after.queue.entries.map((entry) => ({ id: entry.track.trackId, source: entry.track.source, duration: entry.track.durationSeconds })))
      .toEqual(before.queue.entries.map((entry) => ({ id: entry.track.trackId, source: entry.track.source, duration: entry.track.durationSeconds })));
    expect({ sources: audio.sources, load: audio.loadCalls, play: audio.playCalls, pause: audio.pauseCalls }).toEqual(audioBefore);
    expect(h.server.commands).toHaveLength(commands);
  });

  it.each(["omitted", "network", "server"] as const)("%s：不在结果里的曲目或失败时，整个队列原样保留", async (outcome) => {
    h.engine.enqueue([playerTrack("一", { trackId: id(1) })]);
    await h.queued(1);
    const before = h.engine.getSnapshot().queue;
    const requests = stubFetch(() => {
      if (outcome === "network") throw new TypeError("网络失败");
      if (outcome === "server") return jsonResponse(503, { error: { code: "database_unavailable", message: "稍后重试" }, request_id: "test" });
      return jsonResponse(200, { items: [summary(id(2))] });
    });
    mount(); refresh(); await settle();
    expect(requests).toHaveLength(1); expect(h.engine.getSnapshot().queue).toBe(before);
  });

  it("新一轮先返回后，旧一轮晚到也不能覆盖新艺人元数据", async () => {
    h.engine.enqueue([playerTrack("一", { trackId: id(1) })]);
    await h.queued(1);
    const old = deferred<Response>(); const fresh = deferred<Response>();
    let call = 0; stubFetch(() => (++call === 1 ? old.promise : fresh.promise));
    mount(); refresh(); refresh();
    fresh.resolve(jsonResponse(200, { items: [summary(id(1), "新一轮")] }));
    await waitFor(() => expect(h.engine.getSnapshot().queue.entries[0]?.track.title).toBe("新一轮"));
    old.resolve(jsonResponse(200, { items: [summary(id(1), "旧一轮")] }));
    await settle(); expect(h.engine.getSnapshot().queue.entries[0]?.track.title).toBe("新一轮");
  });

  it("查看者代次已失效时，晚到摘要不能写回队列", async () => {
    h.engine.enqueue([playerTrack("一", { trackId: id(1) })]);
    await h.queued(1);
    const before = h.engine.getSnapshot().queue; const pending = deferred<Response>();
    stubFetch(() => pending.promise); mount(); refresh();
    context.scope.isCurrent = () => false;
    pending.resolve(jsonResponse(200, { items: [summary(id(1))] }));
    await settle(); expect(h.engine.getSnapshot().queue).toBe(before);
  });
});
