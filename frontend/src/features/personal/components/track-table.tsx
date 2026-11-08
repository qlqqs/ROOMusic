import { Link } from "@tanstack/react-router";
import type { CSSProperties, ReactNode, Ref } from "react";
import { cx } from "../../../shared/lib/class-names";
import { formatDuration } from "../../../shared/lib/duration";
import { formatCount } from "../../../shared/lib/number";
import { StatusTag, VisuallyHidden } from "../../../shared/ui";
import { albumArtistOf, albumArtistRefsOf, ArtistNames, trackArtistOf, trackArtistRefsOf, trackTitleOf, type TrackSummaryDTO } from "../../catalog";
import styles from "./track-table.module.css";

type TrackTableProps = {
  /** 表格的可访问名称。 */
  label: string;
  /** 歌单可以拖动排序：多出最前面的把手列。 */
  sortable?: boolean;
  /** 表体的行（TrackTableRow）；歌单在外面再包一层拖动排序。 */
  children: ReactNode;
};

/**
 * 我喜欢的音乐与歌单详情共用的曲目表：[把手]、#、标题、专辑艺人、专辑、发行、操作、时长，带表头。
 * 用 div 加表格角色：各行用 subgrid 与表头对齐，窄容器（< 640px）折叠成“标题 + 次要信息”两行，操作与时长留在右边，不横向滚动。
 */
export function TrackTable({ label, sortable = false, children }: TrackTableProps) {
  return (
    <div className={styles.container}>
      <div role="table" aria-label={label} className={cx(styles.table, sortable && styles.sortable)}>
        <div role="rowgroup" className={styles.group}>
          <div role="row" className={cx(styles.row, styles.head)}>
            {sortable ? (
              <div role="columnheader">
                <VisuallyHidden>调整顺序</VisuallyHidden>
              </div>
            ) : null}
            <div role="columnheader" className={styles.number}>
              <span aria-hidden="true">#</span>
              <VisuallyHidden>序号</VisuallyHidden>
            </div>
            <div role="columnheader">标题</div>
            <div role="columnheader">专辑艺人</div>
            <div role="columnheader">专辑</div>
            <div role="columnheader">发行</div>
            <div role="columnheader">
              <VisuallyHidden>操作</VisuallyHidden>
            </div>
            <div role="columnheader" className={styles.duration}>
              时长
            </div>
          </div>
        </div>
        <div role="rowgroup" className={styles.group}>
          {children}
        </div>
      </div>
    </div>
  );
}

type TrackTableRowProps = {
  track: TrackSummaryDTO;
  /** 序号：收藏是按页偏移的连续编号，歌单是歌单里的位置。 */
  number: number;
  /** 播放、心形与“更多”：由 app 组合。 */
  actions: ReactNode;
  /** 歌单的拖动把手；只在 sortable 的表里给。 */
  handle?: ReactNode;
  ref?: Ref<HTMLDivElement>;
  style?: CSSProperties;
  dragging?: boolean;
};

/** 曲目艺人与专辑艺人不同时，标题下小字写曲目艺人（与专辑页规则相同）。 */
function showsTrackArtist(track: TrackSummaryDTO): boolean {
  const artist = track.artist.trim();
  return artist !== "" && artist !== albumArtistOf(track.release);
}

export function TrackTableRow({ track, number, actions, handle, ref, style, dragging = false }: TrackTableRowProps) {
  const { release } = track;
  return (
    <div ref={ref} role="row" className={styles.row} style={style} data-dragging={dragging ? "" : undefined} data-missing={track.present ? undefined : ""}>
      {handle === undefined ? null : (
        <div role="cell" className={styles.handle}>
          {handle}
        </div>
      )}
      <div role="cell" className={styles.number}>
        {formatCount(number)}
      </div>
      <div role="rowheader" className={styles.title}>
        <span className={styles.name}>{trackTitleOf(track)}</span>
        {showsTrackArtist(track) ? (
          <span className={styles.artist}>
            <ArtistNames text={trackArtistOf(track)} refs={trackArtistRefsOf(track)} />
          </span>
        ) : null}
        {track.present ? null : (
          <span className={styles.tag}>
            <StatusTag tone="outline">文件缺失</StatusTag>
          </span>
        )}
      </div>
      {/* 宽容器里 display: contents，三个单元格直接对齐表头；窄容器里成为标题下的第二行。 */}
      <div className={styles.meta}>
        <div role="cell">
          <ArtistNames text={albumArtistOf(release)} refs={albumArtistRefsOf(release)} />
        </div>
        <div role="cell">
          {track.present ? (
            <Link to="/albums/$groupId" params={{ groupId: track.group_id }} search={{ release: release.id }}>
              {release.title}
            </Link>
          ) : (
            release.title
          )}
        </div>
        <div role="cell" className={styles.year}>
          {release.year}
        </div>
      </div>
      <div role="cell" className={styles.actions}>
        {actions}
      </div>
      <div role="cell" className={styles.duration}>
        {track.duration_seconds === null ? (
          <>
            <span aria-hidden="true">{formatDuration(null)}</span>
            <VisuallyHidden>时长未知</VisuallyHidden>
          </>
        ) : (
          formatDuration(track.duration_seconds)
        )}
      </div>
    </div>
  );
}
