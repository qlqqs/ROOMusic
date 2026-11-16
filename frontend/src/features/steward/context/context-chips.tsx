import { Eye, X } from "lucide-react";
import type { StewardPageContext, StewardPlaybackContext } from "./context";
import styles from "./context-chips.module.css";

type Chip = { key: "page" | "playback"; prefix: string; text: string };

function chipsOf(page: StewardPageContext | null, playback: StewardPlaybackContext | null): Chip[] {
  const chips: Chip[] = [];
  if (page !== null) chips.push({ key: "page", prefix: "正在看", text: page.title ?? page.path });
  if (playback !== null) {
    chips.push(
      playback.title !== undefined
        ? { key: "playback", prefix: "正在播放", text: playback.title }
        : { key: "playback", prefix: "播放设备", text: playback.zone_name ?? "当前设备" },
    );
  }
  return chips;
}

/**
 * 输入框上方的上下文标签：“正在看：…”“正在播放：…”（分区里没有曲目时是“播放设备：…”），这条消息会带上它们。
 * 每个标签可以点掉，只对当前草稿有效；没有标签时不渲染。标题来自文件标签，只按文本节点渲染。
 */
export function ContextChips({
  page,
  playback,
  onDismiss,
}: {
  page: StewardPageContext | null;
  playback: StewardPlaybackContext | null;
  onDismiss: (key: "page" | "playback") => void;
}) {
  const chips = chipsOf(page, playback);
  if (chips.length === 0) return null;
  return (
    <ul className={styles.ctx} aria-label="这条消息附带的信息">
      {chips.map((chip) => (
        <li key={chip.key} className={styles.chip}>
          <Eye size={12} aria-hidden="true" />
          <span className={styles.text} title={`${chip.prefix}：${chip.text}`}>
            {`${chip.prefix}：${chip.text}`}
          </span>
          <button type="button" className={styles.remove} aria-label={`移除“${chip.prefix}”`} onClick={() => onDismiss(chip.key)}>
            <X size={12} aria-hidden="true" />
          </button>
        </li>
      ))}
    </ul>
  );
}
