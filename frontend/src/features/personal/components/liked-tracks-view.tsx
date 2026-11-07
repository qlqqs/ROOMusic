import { Link, useNavigate } from "@tanstack/react-router";
import { useMemo, type ReactNode } from "react";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { formatDateTime } from "../../../shared/lib/time";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Button, DataTable, FailureNotice, Pagination, Panel, Skeleton, StatusTag, type Column } from "../../../shared/ui";
import { ArtistNames, trackArtistOf, trackArtistRefsOf, trackTitleOf } from "../../catalog";
import type { FavoriteTrackDTO } from "../api/decoders";
import { useFavoriteTracks } from "../hooks/use-favorites";
import { useRetainedRows } from "../hooks/use-retained-rows";
import { FavoriteStatusScope } from "./favorite-status-scope";
import { UpdatedHint, useCarriedRevision, useResultsTop } from "./favorite-list-parts";
import styles from "./favorites-view.module.css";

type LikedTracksViewProps = {
  /** URL 里的页码，从 1 起。 */
  page: number;
  /** 列表顶部的“播放全部”：由 app 组合播放器动作。 */
  renderPlayAll: () => ReactNode;
  /** 曲目一行的播放、心形与“更多”：由 app 组合（传入稳定的函数引用）。 */
  renderTrackActions: (item: FavoriteTrackDTO) => ReactNode;
};

/** 地址里的页码只在 2 及以上时出现。 */
const pageSearch = (page: number) => ({ page: page > 1 ? page : undefined });

/**
 * 我喜欢的音乐：收藏的曲目，按收藏时间倒序。在这一页里取消收藏后，这一行留在原处、心形变空心，离开这一页或按“刷新”后才消失。
 * 翻页时带上第一页的集合版本，期间收藏有变化就重新读这一页，不把两个版本的结果拼在一起。
 */
export function LikedTracksView({ page, renderPlayAll, renderTrackActions }: LikedTracksViewProps) {
  const carried = useCarriedRevision(page);
  const list = useFavoriteTracks(page, carried.revision);
  const changed = carried.dropIfChanged(list.error);
  const navigate = useNavigate();
  const { headingRef, showTop } = useResultsTop();
  const ready = list.data !== undefined && !list.isPlaceholderData && !list.isError;
  const retained = useRetainedRows(ready ? list.data?.items : undefined, (item) => item.track.track_id, `tracks:${page}`);
  const total = list.data?.pagination.total ?? 0;
  const pageCount = pageCountOf(total, list.data?.pagination.page_size ?? 1);
  // 这一页还留着刚取消收藏的行时不规范化页码：末页的收藏全部取消后，这些行仍留在原处，直到离开或刷新。
  const outOfRange = useClampedPage({
    page,
    pageCount,
    ready: ready && retained.rows.length === 0,
    onOutOfRange: (target) => void navigate({ to: "/me/liked", search: pageSearch(target), replace: true }),
  });
  const trackIds = useMemo(() => retained.rows.map((item) => item.track.track_id), [retained.rows]);

  const columns = useMemo(
    (): Column<FavoriteTrackDTO>[] => [
      {
        id: "track",
        header: "曲目",
        rowHeader: true,
        cell: (item) => (
          <div className={item.track.present ? undefined : styles.missing}>
            <span className={styles.title}>{trackTitleOf(item.track)}</span>
            <span className={styles.artist}>
              <ArtistNames text={trackArtistOf(item.track)} refs={trackArtistRefsOf(item.track)} />
            </span>
            {item.track.present ? null : (
              <span className={styles.tag}>
                <StatusTag tone="outline">文件缺失</StatusTag>
              </span>
            )}
          </div>
        ),
      },
      {
        id: "album",
        header: "专辑",
        cell: (item) => (
          <div className={item.track.present ? undefined : styles.missing}>
            {item.track.present ? (
              <Link to="/albums/$groupId" params={{ groupId: item.track.group_id }} search={{ release: item.track.release.id }}>
                {item.track.release.title}
              </Link>
            ) : (
              item.track.release.title
            )}
          </div>
        ),
      },
      {
        id: "created-at",
        header: "收藏时间",
        cell: (item) => (
          <div className={item.track.present ? undefined : styles.missing}>
            <time dateTime={item.created_at} className={styles.time}>
              {formatDateTime(item.created_at)}
            </time>
          </div>
        ),
      },
      { id: "actions", header: "操作", hideLabelWhenStacked: true, besideRowHeader: true, cell: (item) => renderTrackActions(item) },
    ],
    [renderTrackActions],
  );

  const failure = list.isError && !changed ? <FailureNotice error={list.error} action={<Button onClick={() => void list.refetch()}>重试</Button>} /> : null;
  return (
    <Panel title="收藏的曲目" headingRef={headingRef}>
      {failure}
      {list.data === undefined || outOfRange ? (
        failure === null ? <Skeleton label="正在加载收藏的曲目" lines={4} /> : null
      ) : total === 0 && retained.rows.length === 0 ? (
        <p>
          还没有收藏的曲目。 <Link to="/albums">去专辑页看看</Link>
        </p>
      ) : (
        <div className={styles.results} aria-busy={list.isPlaceholderData || changed}>
          {renderPlayAll()}
          {retained.hasNew ? <UpdatedHint onRefresh={retained.refresh} /> : null}
          {retained.rows.length === 0 ? (
            <p>
              这一页没有收藏。{" "}
              <Link to="/me/liked" search={{}}>
                回到第一页
              </Link>
            </p>
          ) : (
            <FavoriteStatusScope trackIds={trackIds}>
              <DataTable caption="收藏的曲目" columns={columns} rows={retained.rows} rowKey={(item) => item.track.track_id} />
            </FavoriteStatusScope>
          )}
          <p className={styles.note}>共 {formatCount(total)} 首曲目</p>
          <Pagination
            label="收藏的曲目分页"
            page={page}
            pageCount={pageCount}
            renderLink={(target, props) => (
              <Link
                to="/me/liked"
                search={pageSearch(target)}
                onClick={() => {
                  if (list.data !== undefined) carried.carry(target, list.data.revision);
                  showTop();
                }}
                {...props}
              />
            )}
          />
        </div>
      )}
    </Panel>
  );
}
