import { Link } from "@tanstack/react-router";
import { TriangleAlert } from "lucide-react";
import { StatusTag } from "../../../shared/ui";
import type { ReleaseGroupSummaryDTO } from "../api/decoders";
import type { AlbumSearch } from "../album-search";
import { albumArtistOf, albumMeta } from "../format";
import { Cover } from "./cover";
import styles from "./album-card.module.css";

type AlbumCardProps = {
  group: ReleaseGroupSummaryDTO;
  /** 当前列表的筛选与页码：随链接带进详情页，“返回专辑列表”据此恢复。 */
  search: AlbumSearch;
};

/**
 * 一个专辑组的卡片：整张卡片是一个链接（里面没有按钮），封面是装饰，标题与艺人在链接名称里。
 * 单版本的组不出现版本字样；只有接口给出多个成员时才显示“N 个版本”，不暗示任何聚合。
 */
export function AlbumCard({ group, search }: AlbumCardProps) {
  const release = group.representative;
  return (
    <li className={styles.item}>
      <Link to="/albums/$groupId" params={{ groupId: group.id }} state={{ albumList: search }} className={styles.card}>
        <Cover artwork={release.artwork} title={release.title} variant="card" />
        <span className={styles.title}>{release.title}</span> <span className={styles.artist}>{albumArtistOf(release)}</span>{" "}
        <span className={styles.meta}>{albumMeta(release)}</span>
        {group.release_count > 1 || group.attention_count > 0 ? (
          <span className={styles.tags}>
            {group.release_count > 1 ? <StatusTag>{group.release_count} 个版本</StatusTag> : null}
            {group.attention_count > 0 ? (
              <StatusTag tone="outline" icon={<TriangleAlert size={12} />}>
                需要关注
              </StatusTag>
            ) : null}
          </span>
        ) : null}
      </Link>
    </li>
  );
}
