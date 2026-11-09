// 系统媒体键与“正在播放”信息的绑定：把本机播放区的命令登记成媒体键的处理，把正在出声的曲目、播放状态与位置告诉系统。
// 何时同步由输出端决定（按渲染流的分段在曲目边界换信息）；这里只负责翻译与位置上报的节流。
import type { MediaSessionActionName, MediaSessionPort } from "../engine/media-session";
import type { PlayerTrack } from "../engine/queue";
import type { PlayerStatus } from "../zones/model";

const POSITION_STATE_INTERVAL_MS = 1_000;
const SEEK_STEP_SECONDS = 10;

const mediaSessionActions: readonly MediaSessionActionName[] = ["play", "pause", "previoustrack", "nexttrack", "stop", "seekto", "seekbackward", "seekforward"];

/** 媒体键对应的本机播放区命令；position 给快退、快进读取曲目内的当前位置（秒）。 */
export type MediaSessionCommands = {
  play: () => void;
  pause: () => void;
  previous: () => void;
  next: () => void;
  stop: () => void;
  seek: (seconds: number) => void;
  position: () => number;
};

export class MediaSessionBinding {
  private readonly session: MediaSessionPort | null;
  /** 封面资源 ID 对应的绝对地址（给系统媒体信息用）。 */
  private readonly artworkUrl: (artworkId: string) => string;
  private readonly now: () => number;
  private lastPositionStateAt = Number.NEGATIVE_INFINITY;

  constructor(session: MediaSessionPort | null, artworkUrl: (artworkId: string) => string, now: () => number) {
    this.session = session;
    this.artworkUrl = artworkUrl;
    this.now = now;
  }

  /** 登记媒体键。 */
  register(commands: MediaSessionCommands): void {
    const session = this.session;
    if (session === null) return;
    const handlers: Record<MediaSessionActionName, (details: { seekTime?: number | undefined }) => void> = {
      play: () => commands.play(),
      pause: () => commands.pause(),
      previoustrack: () => commands.previous(),
      nexttrack: () => commands.next(),
      stop: () => commands.stop(),
      seekto: (details) => {
        if (details.seekTime !== undefined) commands.seek(details.seekTime);
      },
      seekbackward: () => commands.seek(commands.position() - SEEK_STEP_SECONDS),
      seekforward: () => commands.seek(commands.position() + SEEK_STEP_SECONDS),
    };
    for (const action of mediaSessionActions) session.setActionHandler(action, handlers[action]);
  }

  /** 取消媒体键，清空系统里的“正在播放”信息。 */
  clear(): void {
    const session = this.session;
    if (session === null) return;
    for (const action of mediaSessionActions) session.setActionHandler(action, null);
    session.setMetadata(null);
    session.setPositionState(null);
    session.setPlaybackState("none");
  }

  /** 正在出声的曲目变化之后：换掉系统里的曲目信息，并清掉旧曲目的位置。 */
  showTrack(track: PlayerTrack | null): void {
    const session = this.session;
    if (session === null) return;
    if (track === null) {
      session.setMetadata(null);
    } else {
      session.setMetadata({
        title: track.title,
        artist: track.artist,
        album: track.albumTitle,
        artwork: track.artworkId === null ? [] : [{ src: this.artworkUrl(track.artworkId) }],
      });
    }
    session.setPositionState(null);
    this.lastPositionStateAt = Number.NEGATIVE_INFINITY;
  }

  showStatus(status: PlayerStatus): void {
    this.session?.setPlaybackState(playbackStateOf(status));
  }

  /** 播放中最多每秒一次把位置告诉系统；时长未知时不设置。 */
  showPosition(positionSeconds: number, durationSeconds: number | null): void {
    const session = this.session;
    if (session === null || durationSeconds === null) return;
    const now = this.now();
    if (now - this.lastPositionStateAt < POSITION_STATE_INTERVAL_MS) return;
    this.lastPositionStateAt = now;
    session.setPositionState({ duration: durationSeconds, position: Math.min(positionSeconds, durationSeconds) });
  }
}

/** 系统只区分没有、暂停与播放：缓冲仍算播放，加载算暂停。 */
function playbackStateOf(status: PlayerStatus): "none" | "paused" | "playing" {
  switch (status) {
    case "idle":
      return "none";
    case "playing":
    case "buffering":
      return "playing";
    case "loading":
    case "paused":
      return "paused";
    default: {
      const unreachable: never = status;
      return unreachable;
    }
  }
}
