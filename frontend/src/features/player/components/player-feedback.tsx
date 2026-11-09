import { RotateCcw } from "lucide-react";
import { Button, VisuallyHidden } from "../../../shared/ui";
import { usePlayer } from "../player-provider";
import { failureLabels, rejectionLabels, statusLabels } from "./player-labels";
import styles from "./player-feedback.module.css";

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
  if (event?.kind === "failures_stopped") message = "连续几首都放不了，已停下。";
  if (event?.kind === "transient_failure") message = "暂时无法读取音频，已暂停，可以重试。";
  if (event?.kind === "no_stream_format") message = "这台设备放不了服务器给的音频格式。";
  if (event?.kind === "autoplay_blocked") message = snapshot.isLocal ? "浏览器阻止了自动播放，请按播放。" : "请在那台设备上点一下播放。";
  if (event?.kind === "queue_restored") message = `已恢复 ${event.restored} 首，保持暂停。${event.dropped > 0 ? `已移除 ${event.dropped} 首找不到的曲目。` : ""}`;
  const rejection = snapshot.rejection;
  return <VisuallyHidden><span role="status">
    <span key={event?.seq}>{message}</span>
    <span key={`rejection-${rejection?.seq}`}>{rejection === null ? "" : rejectionLabels[rejection.reason]}</span>
  </span></VisuallyHidden>;
}
/**
 * 播放栏与全屏里的一行状态：命令被拒（目标设备已离线）时先说这个；暂时性故障写原因并给“重试播放”图标按钮，被拒的自动播放写“请按播放”（远端写“请在那台设备上点一下播放”），
 * 当前条目失败写原因，整个队列都不能播放或连续失败停下时说一句；showStatus 时没有问题也写出加载与缓冲。都没有时不渲染。
 */
export function PlayerFaultInline({ showStatus = false, className }: { showStatus?: boolean; className?: string | undefined }) {
  const { engine, snapshot } = usePlayer();
  const fault = snapshot.fault?.kind;
  const currentFailure = snapshot.current === null ? undefined : snapshot.failures[snapshot.current.entryId];
  const retryable = snapshot.rejection === null && fault === "transient";
  const text = snapshot.rejection !== null ? rejectionLabels[snapshot.rejection.reason]
    : fault === "transient" ? "读取失败"
    : fault === "autoplay_blocked" ? (snapshot.isLocal ? "请按播放" : "请在那台设备上点一下播放")
    : fault === "no_stream_format" ? "这台设备放不了服务器给的音频格式。"
    : currentFailure !== undefined ? failureLabels[currentFailure]
    : snapshot.event?.kind === "all_failed" ? "队列里的曲目都不能播放，换一张专辑试试。"
    : snapshot.event?.kind === "failures_stopped" ? "连续几首都放不了，已停下。"
    : showStatus && (snapshot.status === "loading" || snapshot.status === "buffering") ? statusLabels[snapshot.status]
    : null;
  if (text === null) return null;
  return <p className={`${styles.inline} ${className ?? ""}`}>
    <span className={styles.text}>{text}</span>
    {retryable ? <Button variant="icon" icon={<RotateCcw size={16} />} label="重试播放" onClick={() => engine.retry()} /> : null}
  </p>;
}
