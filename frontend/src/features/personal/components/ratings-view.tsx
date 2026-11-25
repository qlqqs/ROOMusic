import { Link, useNavigate } from "@tanstack/react-router";
import { ArrowDownWideNarrow, ClockArrowDown, Star } from "lucide-react";
import { useMemo, type ReactNode } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Button, EmptyState, FailureNotice, Pagination, Skeleton, StatusTag } from "../../../shared/ui";
import { albumMeta, AlbumGridLayout, AlbumGridSkeleton, artistMetaText, Cover, useCatalogState } from "../../catalog";
import type { RatedAlbumDTO, RatedArtistDTO, RatedTrackDTO } from "../api/decoders";
import type { RatingFilter as Filter, RatingSort } from "../api/ratings";
import { useRatedAlbums, useRatedArtists, useRatedTracks } from "../hooks/use-rated-lists";
import { FavoriteStatusScope } from "./favorite-status-scope";
import { RatingControl } from "./rating-control";
import { minRatingText, RatingFilter } from "./rating-filter";
import { RatingStatusScope } from "./rating-status-scope";
import { TrackTable, TrackTableRow } from "./track-table";
import favoriteStyles from "./favorites-view.module.css";
import styles from "./ratings-view.module.css";

export type RatingsTab = "tracks" | "albums" | "artists";

export type RatingsState = { tab: RatingsTab; minRating: number; sort: RatingSort; page: number };

/** 地址：默认值（曲目、1 星、按评分、第 1 页）不写进地址。 */
export function ratingsSearch({ tab, minRating, sort, page }: RatingsState) {
  return {
    tab: tab === "tracks" ? undefined : tab,
    min: minRating > 1 ? minRating : undefined,
    sort: sort === "rating" ? undefined : sort,
    page: page > 1 ? page : undefined,
  };
}

const tabs: readonly { tab: RatingsTab; label: string; unit: string }[] = [
  { tab: "tracks", label: "曲目", unit: "首曲目" },
  { tab: "albums", label: "专辑", unit: "张专辑" },
  { tab: "artists", label: "艺人", unit: "位艺人" },
];

const nounOf = (tab: RatingsTab) => tabs.find((item) => item.tab === tab)?.label ?? "";
const unitOf = (tab: RatingsTab) => tabs.find((item) => item.tab === tab)?.unit ?? "";

type RatingsViewProps = RatingsState & {
  /** 曲目栏的“播放全部”（播放当前筛选与排序下的曲目）：由 app 组合播放器动作。 */
  renderPlayAll: (filter: Filter) => ReactNode;
  /** 曲目一行的播放、心形与“更多”：由 app 组合（传入稳定的函数引用）。 */
  renderTrackActions: (item: RatedTrackDTO) => ReactNode;
};

/**
 * 我的评分：曲目、专辑、艺人三栏，星级筛选（N 星及以上）与排序（按评分 / 最近评分）在栏下的工具行；栏、筛选、排序与页码都写进地址。
 * 每项显示本人星级并可直接改：改完后评分的查询失效，这一页按新值重新读取，条目会移位或被筛掉（不像收藏页那样留在原处）。
 */
export function RatingsView({ tab, minRating, sort, page, renderPlayAll, renderTrackActions }: RatingsViewProps) {
  const navigate = useNavigate();
  // “艺人”一栏只在后端有艺人能力时出现，与侧栏的“艺人”入口一致。
  const artistsState = useCatalogState().data?.artists_state;
  const artistsAvailable = artistsState === "preparing" || artistsState === "ready";
  const filter: Filter = { minRating, sort };
  const go = (next: Partial<RatingsState>) => void navigate({ to: "/me/ratings", search: ratingsSearch({ tab, minRating, sort, page: 1, ...next }) });
  const sortLabel = sort === "rating" ? "排序：按评分" : "排序：最近评分";
  return (
    <div className={favoriteStyles.view}>
      <nav aria-label="评分分类">
        <ul className={favoriteStyles.switchList}>
          {tabs
            .filter((item) => item.tab !== "artists" || artistsAvailable)
            .map((item) => (
              <li key={item.tab}>
                <Link
                  to="/me/ratings"
                  search={ratingsSearch({ tab: item.tab, minRating, sort, page: 1 })}
                  className={favoriteStyles.switchLink}
                  aria-current={item.tab === tab ? "page" : undefined}
                  activeOptions={{ exact: true, includeSearch: false }}
                >
                  {item.label}
                </Link>
              </li>
            ))}
        </ul>
      </nav>
      <div className={styles.tools}>
        <RatingFilter value={minRating} onChange={(next) => go({ minRating: next })} />
        <span className={styles.grow} />
        <Button
          variant="icon"
          icon={sort === "rating" ? <ArrowDownWideNarrow size={16} /> : <ClockArrowDown size={16} />}
          label={sortLabel}
          tooltip={`${sortLabel}（点一下改为${sort === "rating" ? "最近评分" : "按评分"}）`}
          onClick={() => go({ sort: sort === "rating" ? "recent" : "rating" })}
        />
        {tab === "tracks" ? renderPlayAll(filter) : null}
      </div>
      {tab === "albums" ? (
        <RatedAlbumsSection key="albums" filter={filter} page={page} />
      ) : tab === "artists" ? (
        <RatedArtistsSection key="artists" filter={filter} page={page} />
      ) : (
        <RatedTracksSection key="tracks" filter={filter} page={page} renderTrackActions={renderTrackActions} />
      )}
    </div>
  );
}

/** 没有评分时的说明：没有筛选时说怎么打分；有筛选时说这个星级以上没有，并给“看全部评过分的”。 */
function RatingsEmpty({ tab, filter }: { tab: RatingsTab; filter: Filter }) {
  if (filter.minRating <= 1) {
    return (
      <EmptyState icon={<Star />} title={`还没有评过分的${nounOf(tab)}`}>
        在专辑、艺人页的头部，或曲目的“更多操作”里打分。
      </EmptyState>
    );
  }
  return (
    <EmptyState
      icon={<Star />}
      title={`没有 ${minRatingText(filter.minRating)}的${nounOf(tab)}`}
      action={
        <Link to="/me/ratings" search={ratingsSearch({ tab, minRating: 1, sort: filter.sort, page: 1 })}>
          看全部评过分的
        </Link>
      }
    >
      换一个星级，或看全部评过分的。
    </EmptyState>
  );
}

/** 一栏的列表外壳：加载、失败、空状态、越界页、页码与“共 N 首”。三栏共用。 */
function RatedSection<T>({
  tab,
  filter,
  page,
  list,
  failureText,
  skeleton,
  children,
}: {
  tab: RatingsTab;
  filter: Filter;
  page: number;
  list: { data: { items: T[]; pagination: { total: number; page_size: number } } | undefined; isError: boolean; error: unknown; isPlaceholderData: boolean; refetch: () => unknown };
  failureText?: ReactNode;
  skeleton: ReactNode;
  children: (items: T[]) => ReactNode;
}) {
  const navigate = useNavigate();
  const ready = list.data !== undefined && !list.isPlaceholderData && !list.isError;
  const total = list.data?.pagination.total ?? 0;
  const pageCount = pageCountOf(total, list.data?.pagination.page_size ?? 1);
  const outOfRange = useClampedPage({
    page,
    pageCount,
    ready: ready && list.data?.items.length === 0,
    onOutOfRange: (target) => void navigate({ to: "/me/ratings", search: ratingsSearch({ tab, ...filter, page: target }), replace: true }),
  });
  const failure =
    failureText !== undefined ? null : list.isError ? <FailureNotice error={list.error} action={<Button onClick={() => void list.refetch()}>重试</Button>} /> : null;
  if (failureText !== undefined) return failureText;
  if (list.data === undefined || outOfRange) return failure ?? skeleton;
  if (total === 0) return <RatingsEmpty tab={tab} filter={filter} />;
  return (
    <div className={favoriteStyles.results} aria-busy={list.isPlaceholderData}>
      {failure}
      {children(list.data.items)}
      <p className={favoriteStyles.note}>
        共 {formatCount(total)} {unitOf(tab)}
      </p>
      <Pagination
        label={`评过分的${nounOf(tab)}分页`}
        page={page}
        pageCount={pageCount}
        renderLink={(target, props) => <Link to="/me/ratings" search={ratingsSearch({ tab, ...filter, page: target })} {...props} />}
      />
    </div>
  );
}

function RatedTracksSection({ filter, page, renderTrackActions }: { filter: Filter; page: number; renderTrackActions: (item: RatedTrackDTO) => ReactNode }) {
  const list = useRatedTracks(filter, page);
  const trackIds = useMemo(() => list.data?.items.map((item) => item.track.track_id) ?? [], [list.data]);
  const offset = (page - 1) * (list.data?.pagination.page_size ?? 0);
  return (
    <RatedSection tab="tracks" filter={filter} page={page} list={list} skeleton={<Skeleton label="正在加载评过分的曲目" lines={4} />}>
      {(items) => (
        <FavoriteStatusScope trackIds={trackIds}>
          <RatingStatusScope trackIds={trackIds}>
            <TrackTable label="评过分的曲目">
              {items.map((item, index) => (
                <TrackTableRow
                  key={item.track.track_id}
                  track={item.track}
                  number={offset + index + 1}
                  actions={
                    // 宽屏：行里是可直接改的星级（动作里的只读小星隐藏）；窄屏放不下，只留只读小星，改分走“更多操作”的“评分…”。
                    <span className={styles.trackActions}>
                      <span className={styles.rowStars}>
                        <RatingControl target={{ kind: "track", id: item.track.track_id, title: item.track.title }} />
                      </span>
                      {renderTrackActions(item)}
                    </span>
                  }
                />
              ))}
            </TrackTable>
          </RatingStatusScope>
        </FavoriteStatusScope>
      )}
    </RatedSection>
  );
}

/** 评过分的一张专辑：同收藏页的卡片，下面是可直接改的星级。暂时不可用时灰色显示、用评分时记下的名字，没有链接。 */
function RatedAlbumCard({ item }: { item: RatedAlbumDTO }) {
  const { album } = item;
  const representative = album.representative;
  const title = album.title.trim() === "" ? (album.available ? "（无标题）" : "暂时不可用的专辑") : album.title;
  const body = (
    <>
      <Cover artwork={representative?.artwork ?? null} title={title} variant="card" />
      <span className={favoriteStyles.cardTitle}>{title}</span> <span className={favoriteStyles.cardArtist}>{album.artist}</span>{" "}
      {representative === null ? null : <span className={favoriteStyles.cardMeta}>{albumMeta(representative)}</span>}
    </>
  );
  return (
    <li className={favoriteStyles.album} data-available={album.available ? undefined : "false"}>
      {album.available ? (
        <Link to="/albums/$groupId" params={{ groupId: album.group_id }} className={favoriteStyles.card}>
          {body}
        </Link>
      ) : (
        <div className={favoriteStyles.card}>
          {body}
          <span className={favoriteStyles.cardTags}>
            <StatusTag tone="outline">暂时不可用</StatusTag>
          </span>
        </div>
      )}
      <RatingControl target={{ kind: "album", id: album.group_id, title }} size="sm" />
    </li>
  );
}

function RatedAlbumsSection({ filter, page }: { filter: Filter; page: number }) {
  const list = useRatedAlbums(filter, page);
  const groupIds = useMemo(() => list.data?.items.map((item) => item.album.group_id) ?? [], [list.data]);
  return (
    <RatedSection tab="albums" filter={filter} page={page} list={list} skeleton={<AlbumGridSkeleton />}>
      {(items) => (
        <RatingStatusScope groupIds={groupIds}>
          <AlbumGridLayout>
            {items.map((item) => (
              <RatedAlbumCard key={item.album.group_id} item={item} />
            ))}
          </AlbumGridLayout>
        </RatingStatusScope>
      )}
    </RatedSection>
  );
}

/** 评过分的一位艺人：名字是去艺人页的链接，右边是可直接改的星级；暂时没有存在的曲目时标“暂时不可用”。 */
function RatedArtistRow({ item }: { item: RatedArtistDTO }) {
  const { artist } = item;
  const meta = artistMetaText(artist);
  return (
    <li className={favoriteStyles.artistRow} data-available={item.available ? undefined : "false"}>
      <span className={favoriteStyles.artistText}>
        <Link to="/artists/$artistId" params={{ artistId: artist.id }} className={favoriteStyles.artistName}>
          {artist.name}
        </Link>
        {meta === "" ? null : <span className={favoriteStyles.cardMeta}>{meta}</span>}
        {item.available ? null : (
          <span className={favoriteStyles.cardTags}>
            <StatusTag tone="outline">暂时不可用</StatusTag>
          </span>
        )}
      </span>
      <RatingControl target={{ kind: "artist", id: artist.id, title: artist.name }} size="sm" />
    </li>
  );
}

function RatedArtistsSection({ filter, page }: { filter: Filter; page: number }) {
  const list = useRatedArtists(filter, page);
  const artistIds = useMemo(() => list.data?.items.map((item) => item.artist.id) ?? [], [list.data]);
  // 艺人数据还在整理：只说一句，不把它显示成“没有评分”。
  const preparing = list.isError && list.data === undefined && isHttpFailure(failureOf(list.error), 503, "artist_catalog_preparing");
  return (
    <RatedSection
      tab="artists"
      filter={filter}
      page={page}
      list={list}
      failureText={preparing ? <p role="status">艺人信息正在整理，请稍后再试。</p> : undefined}
      skeleton={<Skeleton label="正在加载评过分的艺人" lines={4} />}
    >
      {(items) => (
        <RatingStatusScope artistIds={artistIds}>
          <ul className={favoriteStyles.artistList}>
            {items.map((item) => (
              <RatedArtistRow key={item.artist.id} item={item} />
            ))}
          </ul>
        </RatingStatusScope>
      )}
    </RatedSection>
  );
}
