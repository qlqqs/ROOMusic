// 系统媒体键与锁屏/通知中心的“正在播放”信息（Media Session API）。浏览器不支持时没有端口，引擎跳过它。
// 端口只做最薄的一层封装：引擎决定何时设置什么，这里负责吞掉浏览器对非法状态抛出的异常（例如位置大于时长）。

export type MediaSessionActionName = "play" | "pause" | "previoustrack" | "nexttrack" | "stop" | "seekto" | "seekbackward" | "seekforward";

export type MediaSessionMetadata = {
  title: string;
  artist: string;
  album: string;
  /** 封面的绝对地址（同源封面接口）；没有封面时为空数组。 */
  artwork: readonly { src: string }[];
};

export type MediaSessionPort = {
  setMetadata: (metadata: MediaSessionMetadata | null) => void;
  setPlaybackState: (state: "none" | "paused" | "playing") => void;
  setPositionState: (state: { duration: number; position: number } | null) => void;
  /** seekto 的 seekTime 取自 details；其他动作不带参数。传入 null 取消该动作的处理。 */
  setActionHandler: (action: MediaSessionActionName, handler: ((details: { seekTime?: number | undefined }) => void) | null) => void;
};

/** 真实浏览器的媒体会话；没有 navigator.mediaSession 时返回 null。 */
export function browserMediaSession(): MediaSessionPort | null {
  if (typeof navigator === "undefined" || !("mediaSession" in navigator)) return null;
  const session = navigator.mediaSession;
  return {
    setMetadata: (metadata) => {
      try {
        session.metadata = metadata === null || typeof MediaMetadata === "undefined" ? null : new MediaMetadata({ title: metadata.title, artist: metadata.artist, album: metadata.album, artwork: [...metadata.artwork] });
      } catch {
        // 部分实现只暴露媒体键，或拒绝元数据；不能阻断播放器。
      }
    },
    setPlaybackState: (state) => {
      try {
        session.playbackState = state;
      } catch {
        // 系统媒体状态不可写时，页面中的真实媒体状态仍有效。
      }
    },
    setPositionState: (state) => {
      try {
        if (state === null) session.setPositionState();
        else session.setPositionState({ duration: state.duration, position: state.position, playbackRate: 1 });
      } catch {
        // 浏览器拒绝不一致的位置（例如刚切歌时位置大于时长）；下一次更新会修正。
      }
    },
    setActionHandler: (action, handler) => {
      try {
        session.setActionHandler(action, handler === null ? null : (details) => handler({ seekTime: details.seekTime ?? undefined }));
      } catch {
        // 有的浏览器不支持某些动作（例如 seekbackward）：该动作没有系统媒体键入口，其余照常。
      }
    },
  };
}
