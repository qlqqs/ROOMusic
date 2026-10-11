import { Link } from "@tanstack/react-router";
import { formatDateTime } from "../../../shared/lib/time";
import { albumArtistOf, Cover } from "../../catalog";
import type { RecentReleaseDTO } from "../api/decoders";
import styles from "./recent-release-card.module.css";

/**
 * 最近播放的一张专辑卡片：整张卡片是一个链接（里面没有按钮），指向播放过的那个版本；封面是装饰，
 * 标题、专辑艺人与上次播放时间都在链接名称里。只显示接口给的事实，不写进度、剩余时间之类没有来源的数字。
 */
export function RecentReleaseCard({ item }: { item: RecentReleaseDTO }) {
  const { release } = item;
  return (
    <li className={styles.item}>
      <Link to="/albums/$groupId" params={{ groupId: item.group_id }} search={{ release: release.id }} className={styles.card}>
        <Cover artwork={release.artwork} title={release.title} variant="card" />
        <span className={styles.title}>{release.title}</span> <span className={styles.artist}>{albumArtistOf(release)}</span>{" "}
        <span className={styles.meta}>
          上次播放{" "}
          <time className={styles.time} dateTime={item.last_played_at}>
            {formatDateTime(item.last_played_at)}
          </time>
        </span>
      </Link>
    </li>
  );
}
