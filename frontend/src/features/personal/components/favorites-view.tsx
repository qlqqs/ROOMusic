import { Link, useNavigate } from "@tanstack/react-router";
import { useMemo, useRef, useState, type ReactNode } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { formatDateTime } from "../../../shared/lib/time";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Button, DataTable, FailureNotice, Pagination, Panel, Skeleton, StatusTag, type Column } from "../../../shared/ui";
import { albumMeta, AlbumGridLayout, AlbumGridSkeleton, ArtistNames, artistMetaText, Cover, trackArtistOf, trackArtistRefsOf, trackTitleOf, useCatalogState } from "../../catalog";
import type { FavoriteAlbumDTO, FavoriteArtistDTO, FavoriteTrackDTO } from "../api/decoders";
import { useFavoriteAlbums, useFavoriteArtists, useFavoriteTracks } from "../hooks/use-favorites";
import { useRetainedRows } from "../hooks/use-retained-rows";
import { FavoriteButton } from "./favorite-button";
import { FavoriteStatusScope } from "./favorite-status-scope";
import styles from "./favorites-view.module.css";

export type FavoritesTab = "albums" | "tracks" | "artists";

type FavoritesViewProps = {
  tab: FavoritesTab;
  /** URL 里的页码，从 1 起；两栏各自翻页。 */
  page: number;
  /** 曲目栏顶部的“播放全部”：由 app 组合播放器动作。 */
  renderPlayAll: () => ReactNode;
  /** 曲目一行的播放、心形与“更多”：由 app 组合（传入稳定的函数引用）。 */
  renderTrackActions: (item: FavoriteTrackDTO) => ReactNode;
};

const tabs: readonly { tab: FavoritesTab; label: string }[] = [
  { tab: "albums", label: "专辑" },
  { tab: "tracks", label: "曲目" },
  { tab: "artists", label: "艺人" },
];

/** 地址里的 tab：专辑是默认，不写进地址。 */
function tabSearch(tab: FavoritesTab, page?: number) {
  return { tab: tab === "albums" ? undefined : tab, page: page !== undefined && page > 1 ? page : undefined };
}

/**
 * “我的音乐”的收藏页签：专辑、曲目与艺人三栏（写进地址），都按收藏时间倒序。在这一页里取消收藏后，这一行留在原处、心形变空心，
 * 离开这一页或按“刷新”后才消失。翻页时带上第一页的集合版本，期间收藏有变化就重新读这一页，不把两个版本的结果拼在一起。
 */
export function FavoritesView({ tab, page, renderPlayAll, renderTrackActions }: FavoritesViewProps) {
  // “艺人”一栏只在后端有艺人能力（整理中或已就绪）时出现，与侧栏的“艺人”入口一致。
  const artistsState = useCatalogState().data?.artists_state;
  const artistsAvailable = artistsState === "preparing" || artistsState === "ready";
  const visibleTabs = artistsAvailable ? tabs : tabs.filter((item) => item.tab !== "artists");
  return (
    <div className={styles.view}>
      <nav aria-label="收藏分类">
        <ul className={styles.switchList}>
          {visibleTabs.map((item) => (
            <li key={item.tab}>
              <Link to="/me/favorites" search={tabSearch(item.tab)} className={styles.switchLink} aria-current={item.tab === tab ? "page" : undefined} activeOptions={{ exact: true, includeSearch: true }}>
                {item.label}
              </Link>
            </li>
          ))}
        </ul>
      </nav>
      {tab === "albums" ? (
        <FavoriteAlbumsSection key="albums" page={page} />
      ) : tab === "artists" ? (
        <FavoriteArtistsSection key="artists" page={page} />
      ) : (
        <FavoriteTracksSection key="tracks" page={page} renderPlayAll={renderPlayAll} renderTrackActions={renderTrackActions} />
      )}
    </div>
  );
}

/**
 * 翻页时带上的集合版本：只对目标页有效。服务端说集合变了（409 favorites_changed）就丢掉它，从当前版本重新读这一页；
 * 这一页里保留的行与“列表有更新”的提示照常起作用。
 */
function useCarriedRevision(page: number) {
  const [carried, setCarried] = useState<{ page: number; revision: number } | null>(null);
  return {
    revision: carried !== null && carried.page === page ? carried.revision : null,
    carry: (target: number, revision: number) => setCarried({ page: target, revision }),
    /** 读取失败时调用：是“集合变了”就丢掉版本并返回 true。 */
    dropIfChanged: (error: unknown) => {
      const changed = isHttpFailure(failureOf(error), 409, "favorites_changed");
      if (changed && carried !== null) setCarried(null);
      return changed;
    },
  };
}

/** 翻页的链接在列表下方：把结果顶部滚进视野并聚焦区块标题，接着 Tab 就是第一行（与专辑列表、历史页相同）。 */
function useResultsTop() {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const showTop = () => {
    const heading = headingRef.current;
    if (heading === null) return;
    heading.scrollIntoView({ block: "start" });
    heading.focus({ preventScroll: true });
  };
  return { headingRef, showTop };
}

function UpdatedHint({ onRefresh }: { onRefresh: () => void }) {
  return (
    <p className={styles.updated}>
      列表有更新。{" "}
      <Button onClick={onRefresh}>
        刷新
      </Button>
    </p>
  );
}

function FavoriteTracksSection({ page, renderPlayAll, renderTrackActions }: Omit<FavoritesViewProps, "tab">) {
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
    onOutOfRange: (target) => void navigate({ to: "/me/favorites", search: tabSearch("tracks", target), replace: true }),
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
              <Link to="/me/favorites" search={tabSearch("tracks")}>
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
                to="/me/favorites"
                search={tabSearch("tracks", target)}
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

/** 收藏的一张专辑：可见时整张卡片是去专辑详情的链接；暂时没有可见版本时灰色显示、标“暂时不可用”，没有链接。心形在卡片下方，不放进链接里。 */
function FavoriteAlbumCard({ item }: { item: FavoriteAlbumDTO }) {
  const { album } = item;
  const representative = album.representative;
  const title = album.title.trim() === "" ? (album.available ? "（无标题）" : "暂时不可用的专辑") : album.title;
  const body = (
    <>
      <Cover artwork={representative?.artwork ?? null} title={title} variant="card" />
      <span className={styles.cardTitle}>{title}</span> <span className={styles.cardArtist}>{album.artist}</span>{" "}
      {representative === null ? null : <span className={styles.cardMeta}>{albumMeta(representative)}</span>}
    </>
  );
  return (
    <li className={styles.album} data-available={album.available ? undefined : "false"}>
      {album.available ? (
        <Link to="/albums/$groupId" params={{ groupId: album.group_id }} className={styles.card}>
          {body}
        </Link>
      ) : (
        <div className={styles.card}>
          {body}
          <span className={styles.cardTags}>
            <StatusTag tone="outline">暂时不可用</StatusTag>
          </span>
        </div>
      )}
      <FavoriteButton target={{ kind: "album", id: album.group_id, title }} />
    </li>
  );
}

function FavoriteAlbumsSection({ page }: { page: number }) {
  const carried = useCarriedRevision(page);
  const list = useFavoriteAlbums(page, carried.revision);
  const changed = carried.dropIfChanged(list.error);
  const navigate = useNavigate();
  const { headingRef, showTop } = useResultsTop();
  const ready = list.data !== undefined && !list.isPlaceholderData && !list.isError;
  const retained = useRetainedRows(ready ? list.data?.items : undefined, (item) => item.album.group_id, `albums:${page}`);
  const total = list.data?.pagination.total ?? 0;
  const pageCount = pageCountOf(total, list.data?.pagination.page_size ?? 1);
  // 这一页还留着刚取消收藏的行时不规范化页码：末页的收藏全部取消后，这些行仍留在原处，直到离开或刷新。
  const outOfRange = useClampedPage({
    page,
    pageCount,
    ready: ready && retained.rows.length === 0,
    onOutOfRange: (target) => void navigate({ to: "/me/favorites", search: tabSearch("albums", target), replace: true }),
  });
  const groupIds = useMemo(() => retained.rows.map((item) => item.album.group_id), [retained.rows]);
  const failure = list.isError && !changed ? <FailureNotice error={list.error} action={<Button onClick={() => void list.refetch()}>重试</Button>} /> : null;
  return (
    <Panel title="收藏的专辑" headingRef={headingRef}>
      {failure}
      {list.data === undefined || outOfRange ? (
        failure === null ? <AlbumGridSkeleton /> : null
      ) : total === 0 && retained.rows.length === 0 ? (
        <p>
          还没有收藏的专辑。 <Link to="/albums">去专辑页看看</Link>
        </p>
      ) : (
        <div className={styles.results} aria-busy={list.isPlaceholderData || changed}>
          {retained.hasNew ? <UpdatedHint onRefresh={retained.refresh} /> : null}
          {retained.rows.length === 0 ? (
            <p>
              这一页没有收藏。{" "}
              <Link to="/me/favorites" search={tabSearch("albums")}>
                回到第一页
              </Link>
            </p>
          ) : (
            <FavoriteStatusScope groupIds={groupIds}>
              <AlbumGridLayout>
                {retained.rows.map((item) => (
                  <FavoriteAlbumCard key={item.album.group_id} item={item} />
                ))}
              </AlbumGridLayout>
            </FavoriteStatusScope>
          )}
          <p className={styles.note}>共 {formatCount(total)} 张专辑</p>
          <Pagination
            label="收藏的专辑分页"
            page={page}
            pageCount={pageCount}
            renderLink={(target, props) => (
              <Link
                to="/me/favorites"
                search={tabSearch("albums", target)}
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

/** 收藏的一位艺人：名字是去艺人页的链接，下面是概况；暂时没有存在的曲目时标“暂时不可用”，仍可取消收藏。心形不放进链接里。 */
function FavoriteArtistRow({ item }: { item: FavoriteArtistDTO }) {
  const { artist } = item;
  const meta = artistMetaText(artist);
  return (
    <li className={styles.artistRow} data-available={item.available ? undefined : "false"}>
      <span className={styles.artistText}>
        <Link to="/artists/$artistId" params={{ artistId: artist.id }} className={styles.artistName}>
          {artist.name}
        </Link>
        {meta === "" ? null : <span className={styles.cardMeta}>{meta}</span>}
        {item.available ? null : (
          <span className={styles.cardTags}>
            <StatusTag tone="outline">暂时不可用</StatusTag>
          </span>
        )}
      </span>
      <FavoriteButton target={{ kind: "artist", id: artist.id, title: artist.name }} />
    </li>
  );
}

/** 艺人数据还在整理：只说一句，不把它显示成“没有收藏”。 */
function isPreparing(error: unknown): boolean {
  return isHttpFailure(failureOf(error), 503, "artist_catalog_preparing");
}

function FavoriteArtistsSection({ page }: { page: number }) {
  const carried = useCarriedRevision(page);
  const list = useFavoriteArtists(page, carried.revision);
  const changed = carried.dropIfChanged(list.error);
  const navigate = useNavigate();
  const { headingRef, showTop } = useResultsTop();
  const ready = list.data !== undefined && !list.isPlaceholderData && !list.isError;
  const retained = useRetainedRows(ready ? list.data?.items : undefined, (item) => item.artist.id, `artists:${page}`);
  const total = list.data?.pagination.total ?? 0;
  const pageCount = pageCountOf(total, list.data?.pagination.page_size ?? 1);
  const outOfRange = useClampedPage({
    page,
    pageCount,
    ready: ready && retained.rows.length === 0,
    onOutOfRange: (target) => void navigate({ to: "/me/favorites", search: tabSearch("artists", target), replace: true }),
  });
  const artistIds = useMemo(() => retained.rows.map((item) => item.artist.id), [retained.rows]);
  const preparing = list.isError && list.data === undefined && isPreparing(list.error);
  const failure = list.isError && !changed && !preparing ? <FailureNotice error={list.error} action={<Button onClick={() => void list.refetch()}>重试</Button>} /> : null;
  return (
    <Panel title="收藏的艺人" headingRef={headingRef}>
      {failure}
      {preparing ? (
        <p role="status">艺人信息正在整理，请稍后再试。</p>
      ) : list.data === undefined || outOfRange ? (
        failure === null ? <Skeleton label="正在加载收藏的艺人" lines={4} /> : null
      ) : total === 0 && retained.rows.length === 0 ? (
        <p>
          还没有收藏的艺人。 <Link to="/artists">去艺人页看看</Link>
        </p>
      ) : (
        <div className={styles.results} aria-busy={list.isPlaceholderData || changed}>
          {retained.hasNew ? <UpdatedHint onRefresh={retained.refresh} /> : null}
          {retained.rows.length === 0 ? (
            <p>
              这一页没有收藏。{" "}
              <Link to="/me/favorites" search={tabSearch("artists")}>
                回到第一页
              </Link>
            </p>
          ) : (
            <FavoriteStatusScope artistIds={artistIds}>
              <ul className={styles.artistList}>
                {retained.rows.map((item) => (
                  <FavoriteArtistRow key={item.artist.id} item={item} />
                ))}
              </ul>
            </FavoriteStatusScope>
          )}
          <p className={styles.note}>共 {formatCount(total)} 位艺人</p>
          <Pagination
            label="收藏的艺人分页"
            page={page}
            pageCount={pageCount}
            renderLink={(target, props) => (
              <Link
                to="/me/favorites"
                search={tabSearch("artists", target)}
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
