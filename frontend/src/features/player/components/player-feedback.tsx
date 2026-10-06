import { Button, Notice, VisuallyHidden } from "../../../shared/ui";
import { usePlayer } from "../player-provider";
import { failureLabels } from "./player-labels";

/**
 * 仅离散事件进入 live region，进度变化不播报；外壳常驻渲染，所以在任何页面都能听到失败与“请按播放”。
 * live region 本身不随事件重建（新插入的 live region 不一定被读屏读出），只按事件序号换掉里面的文字，同样的文字再次发生也会再读。
 */
export function PlayerAnnouncement() {
  const { snapshot } = usePlayer();
  const event = snapshot.event;
  let message = "";
  if (event?.kind === "track_started") message = `已选择曲目：${snapshot.queue.entries.find((entry) => entry.entryId === event.entryId)?.track.title ?? "曲目"}`;
  if (event?.kind === "track_failed") message = failureLabels[event.reason];
  if (event?.kind === "all_failed") message = "队列中的曲目都无法播放。";
  if (event?.kind === "transient_failure") message = "暂时无法读取音频，已暂停。请检查网络后到正在播放页重试。";
  if (event?.kind === "autoplay_blocked") message = "浏览器阻止了自动播放，请按播放。";
  if (event?.kind === "queue_restored") message = `已恢复 ${event.restored} 首，保持暂停。${event.dropped > 0 ? `已移除 ${event.dropped} 首不可见的曲目。` : ""}`;
  return <VisuallyHidden><span role="status"><span key={event?.seq}>{message}</span></span></VisuallyHidden>;
}
export function PlayerFeedback() {
  const { engine, snapshot } = usePlayer();
  if (snapshot.fault?.kind === "transient") return <Notice tone="warning" action={<Button onClick={() => engine.retry()}>重试播放</Button>}>暂时无法读取音频，请检查网络后重试。</Notice>;
  if (snapshot.fault?.kind === "autoplay_blocked") return <Notice>浏览器阻止了自动播放，请按播放。</Notice>;
  if (snapshot.event?.kind === "all_failed") return <Notice tone="warning">队列中的曲目都无法播放，请查看各项原因或换一张专辑。</Notice>;
  return null;
}
