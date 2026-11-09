import { afterEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import { FakeAudio } from "../../../test/fake-audio";
import { fakeZoneServer } from "../../../test/fake-zones";
import { playerTrack } from "../../../test/player-data";
import type { MediaSessionPort } from "../engine/media-session";
import type { Zone, ZoneStream } from "../zones/model";
import { MediaSessionBinding } from "./media-session-binding";
import { OutputEndpoint } from "./output";

const endpoints: OutputEndpoint[] = [];
afterEach(() => {
  for (const endpoint of endpoints.splice(0)) endpoint.stop();
});

function stream(seq: number): ZoneStream {
  return {
    seq, url: `/api/v1/me/playback/zones/test/streams/${seq}`, mimeType: "audio/wav", quality: "lossless",
    format: { codec: "wav", sampleRate: 44100, bitDepth: 16, channels: 2 },
    segments: [{ entryId: `entry-${seq}`, track: playerTrack(String(seq)), streamStartSeconds: 0, trackStartSeconds: 0 }],
  };
}

async function harness() {
  const server = fakeZoneServer();
  const audios: FakeAudio[] = [];
  const metadata = vi.fn<MediaSessionPort["setMetadata"]>();
  const report = vi.fn(server.api.postOutputReport);
  const endpoint = new OutputEndpoint({
    claim: (body) => server.api.claimOutput(server.device().deviceId, body),
    report, release: server.api.releaseOutput,
    // 单元测试显式投递期望状态，不让替身服务端的旧响应干扰指定的事件顺序。
    onResult: () => undefined, onSession: () => undefined,
    onDeviceMissing: () => undefined, onSuperseded: () => undefined, onTime: () => undefined,
    createAudio: () => { const audio = new FakeAudio(); audios.push(audio); return audio; },
    mediaSession: new MediaSessionBinding({
      setMetadata: metadata, setPlaybackState: () => undefined,
      setPositionState: () => undefined, setActionHandler: () => undefined,
    }, (id) => `https://roomusic.test/artwork/${id}`, Date.now),
    commands: { play: vi.fn(), pause: vi.fn(), previous: vi.fn(), next: vi.fn(), stop: vi.fn(), seek: vi.fn(), position: () => 0 },
    now: Date.now,
  });
  endpoints.push(endpoint);
  endpoint.start(false);
  await waitFor(() => expect(endpoint.session).not.toBeNull());
  const align = (patch: Partial<Zone>) => endpoint.align({ ...server.zone(), revision: 10, intent: "play", ...patch });
  const audioFor = (source: ZoneStream) => {
    const audio = audios.find((candidate) => candidate.src === source.url);
    if (audio === undefined) throw new Error(`没有加载流 ${source.seq}`);
    return audio;
  };
  const start = (source: ZoneStream) => {
    align({ stream: source });
    const audio = audioFor(source);
    audio.beginPlaying();
    return audio;
  };
  return { endpoint, audios, metadata, report, align, audioFor, start };
}

describe("输出端对齐", () => {
  it("新流出声后才卸载旧流并回报新序号", async () => {
    const h = await harness();
    const old = h.start(stream(1));
    const next = stream(2);
    h.align({ revision: 11, stream: next });
    const incoming = h.audioFor(next);
    expect(old.paused).toBe(false);
    expect(old.src).not.toBe("");
    incoming.beginPlaying();
    expect(old.src).toBe("");
    expect(incoming.paused).toBe(false);
    await waitFor(() => expect(h.report).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ kind: "status", stream_seq: 2 })));
  });

  it.each(["next_then_ended", "ended_then_next"] as const)("接续流在两种到达顺序都换上：%s", async (order) => {
    const h = await harness();
    const current = stream(1);
    const next = stream(2);
    const audio = h.start(current);
    if (order === "next_then_ended") {
      h.align({ revision: 11, stream: current, nextStream: next });
      expect(h.audioFor(next).playCalls).toBe(0);
      audio.finish();
    } else {
      audio.finish();
      h.align({ revision: 11, stream: current, nextStream: next });
    }
    expect(h.audioFor(next).paused).toBe(false);
    expect(audio.src).toBe("");
    await waitFor(() => expect(h.report).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ kind: "status", stream_seq: 2 })));
  });

  it("同一条流随播放和暂停意图切换而不重新加载", async () => {
    const h = await harness();
    const current = stream(1);
    h.align({ stream: current, intent: "pause" });
    const audio = h.audioFor(current);
    expect(audio.paused).toBe(true);
    expect(audio.playCalls).toBe(0);
    h.align({ revision: 11, stream: current });
    expect(audio.paused).toBe(false);
    h.align({ revision: 12, stream: current, intent: "pause" });
    expect(audio.paused).toBe(true);
    expect(audio.sources).toEqual([current.url]);
  });

  it("丢弃旧 revision，不换流也不接受旧暂停意图", async () => {
    const h = await harness();
    const audio = h.start(stream(2));
    h.align({ revision: 9, stream: stream(1), intent: "pause" });
    expect(audio.paused).toBe(false);
    expect(h.audios.flatMap((element) => element.sources)).toEqual([stream(2).url]);
  });

  it("曲目中途错误回报 stream_lost，不换上接续流", async () => {
    const h = await harness();
    const current = stream(1);
    const next = stream(2);
    const audio = h.start(current);
    audio.tick(12);
    h.align({ revision: 11, stream: current, nextStream: next });
    audio.fail(3);
    h.align({ revision: 12, stream: current, nextStream: next });
    expect(h.audioFor(next).playCalls).toBe(0);
    expect(audio.src).toBe(current.url);
    await waitFor(() => expect(h.report).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ kind: "fault", fault: "stream_lost", stream_seq: 1, stream_seconds: 12 })));
  });

  it("按 segments 边界同时更新锁屏标题、艺人、专辑与封面", async () => {
    const h = await harness();
    const first = playerTrack("一", { artist: "艺人一", albumTitle: "专辑一", artworkId: "cover-1" });
    const second = playerTrack("二", { artist: "艺人二", albumTitle: "专辑二", artworkId: "cover-2" });
    const current: ZoneStream = { ...stream(1), segments: [
      { entryId: "entry-1", track: first, streamStartSeconds: 0, trackStartSeconds: 0 },
      { entryId: "entry-2", track: second, streamStartSeconds: 20, trackStartSeconds: 0 },
    ] };
    const audio = h.start(current);
    expect(h.metadata).toHaveBeenLastCalledWith({ title: first.title, artist: first.artist, album: first.albumTitle, artwork: [{ src: "https://roomusic.test/artwork/cover-1" }] });
    audio.tick(20);
    expect(h.metadata).toHaveBeenLastCalledWith({ title: second.title, artist: second.artist, album: second.albumTitle, artwork: [{ src: "https://roomusic.test/artwork/cover-2" }] });
  });

  it("同一条流追加了下一首的分段后，锁屏信息在边界换成下一首", async () => {
    const h = await harness();
    const first = playerTrack("一");
    const second = playerTrack("二");
    const opening: ZoneStream = { ...stream(1), segments: [{ entryId: "entry-1", track: first, streamStartSeconds: 0, trackStartSeconds: 0 }] };
    const audio = h.start(opening);
    expect(h.metadata).toHaveBeenLastCalledWith(expect.objectContaining({ title: first.title }));
    // 服务端在同一条流（同一个 seq）里追加第二首的分段，元素不重新加载。
    h.align({ revision: 11, stream: { ...opening, segments: [...opening.segments, { entryId: "entry-2", track: second, streamStartSeconds: 20, trackStartSeconds: 0 }] } });
    expect(audio.sources).toEqual([opening.url]);
    audio.tick(20);
    expect(h.metadata).toHaveBeenLastCalledWith(expect.objectContaining({ title: second.title }));
  });

  it("首曲打不开的空流未触发 playing 就出错，仍能换上晚到的接续流", async () => {
    const h = await harness();
    const empty: ZoneStream = { ...stream(1), format: null, segments: [] };
    const next = stream(2);
    h.align({ stream: empty });
    const audio = h.audioFor(empty);
    // 没有可解码音频，浏览器不会先发 playing；只会报 error。
    audio.fail(4);
    h.align({ revision: 11, stream: empty, nextStream: next });
    expect(h.audios.some((element) => element.src === next.url && !element.paused)).toBe(true);
    expect(audio.sources.filter((source) => source === empty.url)).toHaveLength(1);
  });
});
