import { statusLabels } from "../components/player-labels";
import { usePlayerSelector } from "../player-provider";

/**
 * 专辑页曲目行的当前曲目标记：任何状态下都标出当前曲目，文字是真实状态（加载中写“正在加载”，不说成正在播放）。
 * 只订阅当前曲目与播放状态两个基本值，音量、队列与进度变化不重绘整张曲目表。
 */
export function useCurrentTrackMarker(): { trackId: string | undefined; label: string | undefined } {
  const trackId = usePlayerSelector((snapshot) => snapshot.current?.track.trackId);
  const status = usePlayerSelector((snapshot) => snapshot.status);
  return { trackId, label: trackId === undefined ? undefined : statusLabels[status] };
}
