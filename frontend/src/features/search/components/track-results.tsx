import { Link } from "@tanstack/react-router";
import { useMemo, type ReactNode } from "react";
import { formatDuration } from "../../../shared/lib/duration";
import { DataTable, StatusTag, type Column } from "../../../shared/ui";
import type { SearchTrackItemDTO } from "../api/decoders";
import { matchNote } from "../match-note";
import { ArtistNames, trackArtistOf, trackArtistRefsOf, trackTitleOf } from "../../catalog";
import styles from "./track-results.module.css";

type TrackResultsProps = {
  items: readonly SearchTrackItemDTO[];
  /** 一行的动作（播放、下一首、加入队列……）：由 app 组合播放器，search 不导入 player。插槽保持同一个函数引用，列定义才不会每次重建。 */
  renderTrackActions: (item: SearchTrackItemDTO) => ReactNode;
};

/**
 * 曲目结果：一首一行（标题与艺人、所在专辑、时长、动作）。命中的不是这一行已经显示的文字时（例如作曲者、厂牌、编号），
 * 标题下方一行小字说明匹配到了什么。全局曲目结果只有当前存在的曲目；万一出现缺失的曲目，照历史页的做法标“文件缺失”、专辑名不做链接。
 */
export function TrackResults({ items, renderTrackActions }: TrackResultsProps) {
  const columns = useMemo(
    (): Column<SearchTrackItemDTO>[] => [
      {
        id: "track",
        header: "曲目",
        rowHeader: true,
        cell: (item) => {
          const title = trackTitleOf(item);
          const artist = trackArtistOf(item);
          const note = matchNote(item.match, [title, artist, item.release.title]);
          return (
            <div className={item.present ? undefined : styles.missing}>
              <span className={styles.title}>{title}</span>
              <span className={styles.artist}>
                <ArtistNames text={artist} refs={trackArtistRefsOf(item)} />
              </span>
              {note === null ? null : <span className={styles.note}>{note}</span>}
              {item.present ? null : (
                <span className={styles.tag}>
                  <StatusTag tone="outline">文件缺失</StatusTag>
                </span>
              )}
            </div>
          );
        },
      },
      {
        id: "album",
        header: "专辑",
        cell: (item) =>
          item.present ? (
            <Link to="/albums/$groupId" params={{ groupId: item.group_id }} search={{ release: item.release.id }}>
              {item.release.title}
            </Link>
          ) : (
            <span className={styles.missing}>{item.release.title}</span>
          ),
      },
      {
        id: "actions",
        header: "操作",
        hideLabelWhenStacked: true,
        besideRowHeader: true,
        cell: (item) => renderTrackActions(item),
      },
      {
        id: "duration",
        header: "时长",
        numeric: true,
        cell: (item) => <span className={styles.duration}>{formatDuration(item.duration_seconds)}</span>,
      },
    ],
    [renderTrackActions],
  );
  return <DataTable caption="曲目结果" columns={columns} rows={items} rowKey={(item) => item.track_id} />;
}
