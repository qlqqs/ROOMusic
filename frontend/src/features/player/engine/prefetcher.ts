// 预取下一首：当前曲目第一次出声之后，为下一首预先发一次预检。预检是唯一会启动转换的请求，所以下一首是需要转换的格式时，
// 转换在当前曲目播放期间完成，切歌时预检立即命中缓存。与当前加载互相独立，结果被丢弃（不改变任何状态）；
// 只发一次，没等到就在切歌时按正常流程等。队列或当前项变化时由引擎中止。
import type { TrackPlaybackResult } from "../api/endpoints";
import { upNext, type PlayerTrack, type QueueState } from "./queue";

export class Prefetcher {
  private readonly loadPlayback: (trackId: string, signal: AbortSignal) => Promise<TrackPlaybackResult>;
  private readonly shouldPrefetch: (track: PlayerTrack) => boolean;
  private controller: AbortController | null = null;

  constructor(loadPlayback: (trackId: string, signal: AbortSignal) => Promise<TrackPlaybackResult>, shouldPrefetch: (track: PlayerTrack) => boolean) {
    this.loadPlayback = loadPlayback;
    this.shouldPrefetch = shouldPrefetch;
  }

  /** 为 queue 的下一首发出预取（先中止上一次）。单曲循环没有“下一首”，随机模式下一首尚未确定，都不预取。 */
  start(queue: QueueState): void {
    this.cancel();
    if (queue.mode === "repeat_one" || queue.mode === "shuffle") return;
    const [next] = upNext(queue, 1);
    if (next === undefined || next.entryId === queue.currentEntryId || !this.shouldPrefetch(next.track)) return;
    const controller = new AbortController();
    this.controller = controller;
    const finished = (): void => {
      if (this.controller === controller) this.controller = null;
    };
    this.loadPlayback(next.track.trackId, controller.signal).then(finished, finished);
  }

  cancel(): void {
    this.controller?.abort();
    this.controller = null;
  }
}
