import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { formatRelativeTime } from "../../../shared/lib/time";
import { VisuallyHidden } from "../../../shared/ui";
import { albumArtistOf, Cover } from "../../catalog";
import type { RecentReleaseDTO } from "../api/decoders";
import styles from "./recent-release-card.module.css";

/**
 * 最近播放的一张专辑卡片：整张卡片是一个链接，指向播放过的那个版本；封面是装饰，标题、专辑艺人与上次播放的相对时间
 * （“3 分钟前”“昨天”，机器可读的完整时间在 time 的 dateTime 里）都在链接名称里。只显示接口给的事实，不写进度、剩余时间之类没有来源的数字。
 * action 是封面右下角的操作（播放键，由 app 组合）：放在链接之外，不嵌套在链接里；鼠标悬停或键盘聚焦到这张卡片时才出现，触屏不显示。
 */
export function RecentReleaseCard({ item, action }: { item: RecentReleaseDTO; action?: ReactNode }) {
  const { release } = item;
  return (
    <li className={styles.item}>
      <Link to="/albums/$groupId" params={{ groupId: item.group_id }} search={{ release: release.id }} className={styles.card}>
        <Cover artwork={release.artwork} title={release.title} variant="card" />
        <span className={styles.title}>{release.title}</span> <span className={styles.artist}>{albumArtistOf(release)}</span>{" "}
        <span className={styles.meta}>
          <VisuallyHidden>上次播放</VisuallyHidden>{" "}
          <time dateTime={item.last_played_at}>{formatRelativeTime(item.last_played_at)}</time>
        </span>
      </Link>
      {action === undefined ? null : <div className={styles.action}>{action}</div>}
    </li>
  );
}
