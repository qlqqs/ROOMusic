import { Link } from "@tanstack/react-router";
import { useEffect, useEffectEvent, useMemo, useRef, useState, type ReactNode } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { formatDuration } from "../../../shared/lib/duration";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { Button, DataTable, Page, Pagination, Panel, QueryContent, Skeleton, StatusTag, VisuallyHidden, type Column } from "../../../shared/ui";
import {
  albumArtistOf,
  albumMeta,
  AlbumGridLayout,
  AlbumGridSkeleton,
  ArtistNames,
  artistRoleLabels,
  Cover,
  trackArtistOf,
  trackArtistRefsOf,
  trackTitleOf,
  type TrackSummaryDTO,
} from "../../catalog";
import { artistTrackRoles, type ArtistAlbumDTO, type ArtistDetailDTO, type ArtistTrackRole } from "../api/decoders";
import type { ArtistDetailSearch } from "../artist-search";
import { useArtistMedia } from "../hooks/use-artist-media";
import { isArtistCatalogPreparing, useArtist, useArtistAlbums, useArtistAppearsOnAlbums, useArtistNames, useArtistTracks } from "../hooks/use-artists";
import { ArtistHeader } from "./artist-header";
import { ArtistPreparing } from "./artist-preparing";
import styles from "./artist-detail-view.module.css";

type ArtistDetailViewProps = {
  /** 路径里的艺人 ID（路由已校验为 UUID 并转成小写）；不是 UUID 时为 null，直接显示“艺人不存在”。 */
  artistId: string | null;
  search: ArtistDetailSearch;
  /** 这位艺人已经被合并进别的艺人：由 app 用替换历史的方式带到现在的艺人页。 */
  onRedirect: (artistId: string) => void;
  /**
   * 头部的操作区（播放全部、随机播放、心形与“更多”，管理员的合并、拆开、改名在“更多”的“管理”分组里，结果提示在操作行下方）：
   * 由 app 组合；后端仍独立鉴权。
   */
  renderHeaderActions: (detail: ArtistDetailDTO) => ReactNode;
  /** 曲目一行的动作（播放、心形、“更多”）：由 app 组合；传入稳定的函数引用。 */
  renderTrackActions: (item: TrackSummaryDTO) => ReactNode;
  /** 包住一页曲目行的范围（这一页的收藏状态一次读出）：由 app 提供；传入稳定的函数引用。 */
  renderTrackRowsScope?: ((items: readonly TrackSummaryDTO[], children: ReactNode) => ReactNode) | undefined;
  /**
   * 管理员：图片、简介与对应写入之后刷新别处缓存的函数（艺人与搜索）。给了就显示管理员的写入入口；普通成员不给。
   * 后端仍独立鉴权。
   */
  onMediaChanged?: (() => Promise<unknown>) | undefined;
};

/** 翻页的链接在列表下方：把这一栏的标题滚进视野并聚焦（与其他列表相同）。 */
function useSectionTop() {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const showTop = () => {
    const heading = headingRef.current;
    if (heading === null) return;
    heading.scrollIntoView({ block: "start" });
    heading.focus({ preventScroll: true });
  };
  return { headingRef, showTop };
}

function ArtistNotFound() {
  return (
    <Page title="艺人不存在" actions={<Link to="/artists">全部艺人</Link>}>
      <p>它可能已被移除，或者地址不正确。</p>
    </Page>
  );
}

/**
 * 艺人页：页头（圆章、主名、角色、概况与操作区）、其他名字、作为专辑艺人的专辑、以其他身份参与的专辑，以及按角色分栏的参与曲目（曲目艺人、作曲、指挥、演奏、制作人）。
 * 专辑、参与的专辑、曲目与名字各自翻页，页码与选中的角色都在地址里。已经合并进别的艺人时跳到现在的艺人页；艺人数据还在整理时只说一句。
 */
export function ArtistDetailView({ artistId, search, onRedirect, renderHeaderActions, renderTrackActions, renderTrackRowsScope, onMediaChanged }: ArtistDetailViewProps) {
  const detail = useArtist(artistId);
  // 正在重新读取时不按缓存里的旧结果跳走（旧的重定向可能已经被拆开撤掉），等读到最新的再决定。
  const redirect = detail.isFetching ? null : (detail.data?.redirect_artist_id ?? null);
  const goToCurrent = useEffectEvent((target: string) => onRedirect(target));
  useEffect(() => {
    if (redirect !== null && redirect !== artistId) goToCurrent(redirect);
  }, [redirect, artistId]);

  if (artistId === null) return <ArtistNotFound />;
  if (detail.isError && detail.data === undefined) {
    if (isHttpFailure(failureOf(detail.error), 404)) return <ArtistNotFound />;
    if (isArtistCatalogPreparing(detail.error)) {
      return (
        <Page title="艺人">
          <ArtistPreparing />
        </Page>
      );
    }
  }
  const data = detail.data;
  // 结果是重定向（不管是正在跳走，还是缓存里的旧结果正在重新读取）时先显示加载中，不把被合并的旧身份当作艺人页显示。
  if (data === undefined || data.redirect_artist_id !== null) {
    return (
      <Page title={data?.artist.name ?? "艺人"}>
        <QueryContent query={detail} loading={<Skeleton label="正在加载艺人" lines={6} />}>
          {() => <Skeleton label="正在打开现在的艺人页" lines={6} />}
        </QueryContent>
      </Page>
    );
  }
  const { artist } = data;
  return (
    <Page
      title={artist.name}
      header={
        onMediaChanged === undefined ? (
          <ArtistHeader artist={artist} compactTitle={artist.name.length > 80} actions={renderHeaderActions(data)} trackRoles={trackRolesOf(data)} image={data.image} bio={data.bio} links={data.links} />
        ) : (
          <AdminArtistHeader key={artist.id} detail={data} actions={renderHeaderActions(data)} onMediaChanged={onMediaChanged} />
        )
      }
    >
      <NamesSection detail={data} search={search} />
      <AlbumsSection artistId={artist.id} search={search} />
      <AppearsOnSection artistId={artist.id} search={search} />
      <TracksSection detail={data} search={search} renderTrackActions={renderTrackActions} renderTrackRowsScope={renderTrackRowsScope} />
    </Page>
  );
}

/** 管理员的页头：多了图片、简介与对应的写入（换艺人时用 key 重新开始，等待中的标记随之提交）。 */
function AdminArtistHeader({ detail, actions, onMediaChanged }: { detail: ArtistDetailDTO; actions: ReactNode; onMediaChanged: () => Promise<unknown> }) {
  const media = useArtistMedia({ artistId: detail.artist.id, revision: detail.revision, refresh: onMediaChanged });
  const { artist } = detail;
  return (
    <ArtistHeader
      artist={artist}
      compactTitle={artist.name.length > 80}
      actions={actions}
      trackRoles={trackRolesOf(detail)}
      image={detail.image}
      bio={detail.bio}
      links={detail.links}
      media={media}
    />
  );
}

/**
 * 其他名字（别名与不同写法）：只有一个名字时不出现。详情里先给出的若干个直接列出；更多时“显示全部”后分页读取（第 2 页起页码在地址里）。
 */
function NamesSection({ detail, search }: { detail: ArtistDetailDTO; search: ArtistDetailSearch }) {
  const total = detail.names_pagination.total;
  const [expanded, setExpanded] = useState(false);
  const paged = expanded || search.names_page !== undefined;
  const names = useArtistNames(detail.artist.id, search.names_page ?? 1, paged && total > detail.names.length);
  const { headingRef, showTop } = useSectionTop();
  if (total <= 1) return null;
  const items = paged && names.data !== undefined ? names.data.items : detail.names;
  const pagination = paged ? names.data?.pagination : undefined;
  return (
    <Panel title="名字" headingRef={headingRef} action={<span>{formatCount(total)} 个</span>}>
      <ul className={styles.names}>
        {items.map((name) => (
          <li key={name.id} className={styles.name}>
            {name.name}
            {name.is_display_name ? <StatusTag>主名</StatusTag> : null}
          </li>
        ))}
      </ul>
      {!paged && total > detail.names.length ? (
        <p>
          <Button onClick={() => setExpanded(true)}>
            显示全部 {formatCount(total)} 个名字
          </Button>
        </p>
      ) : null}
      {pagination === undefined ? null : (
        <Pagination
          label="名字分页"
          page={pagination.page}
          pageCount={pageCountOf(pagination.total, pagination.page_size)}
          renderLink={(target, props) => (
            <Link to="/artists/$artistId" params={{ artistId: detail.artist.id }} search={(previous) => ({ ...previous, names_page: target > 1 ? target : undefined })} onClick={showTop} {...props} />
          )}
        />
      )}
    </Panel>
  );
}

/**
 * 一张专辑：与专辑库同一种卡片（整张卡片是链接，卡片上的艺人名是文字）；打开时选中这位艺人署名的那个版本。
 * meta 是标题下的一行：专辑栏写年份与曲目数，参与的专辑写专辑艺人（这张专辑是谁的）。
 */
function ArtistAlbumCard({ item, meta }: { item: ArtistAlbumDTO; meta: string }) {
  const release = item.group.representative;
  const credited = item.credited_release_id === release.id ? undefined : item.credited_release_id;
  return (
    <li className={styles.album}>
      <Link to="/albums/$groupId" params={{ groupId: item.group.id }} search={{ release: credited }} className={styles.card}>
        <Cover artwork={release.artwork} title={release.title} variant="card" />
        <span className={styles.cardTitle}>{release.title}</span> <span className={styles.cardMeta}>{meta}</span>
        {item.group.release_count > 1 ? (
          <span className={styles.cardTags}>
            <StatusTag>{item.group.release_count} 个版本</StatusTag>
          </span>
        ) : null}
      </Link>
    </li>
  );
}

type ShelfPageKey = "albums_page" | "appears_page";
type ShelfQuery = ReturnType<typeof useArtistAlbums> | ReturnType<typeof useArtistAppearsOnAlbums>;

/** 一栏专辑卡片：标题旁是张数，翻页写进地址（pageKey）；第一页就没有时整栏不出现。 */
function AlbumShelf({ artistId, title, pageKey, query, metaOf }: { artistId: string; title: string; pageKey: ShelfPageKey; query: ShelfQuery; metaOf: (item: ArtistAlbumDTO) => string }) {
  const { headingRef, showTop } = useSectionTop();
  const data = query.data;
  if (data !== undefined && data.pagination.total === 0 && data.pagination.page === 1) return null;
  return (
    <Panel title={title} headingRef={headingRef} action={data === undefined ? undefined : <span>{formatCount(data.pagination.total)} 张</span>}>
      <QueryContent query={query} loading={<AlbumGridSkeleton count={6} />}>
        {(page) =>
          page.items.length === 0 ? (
            <p>
              这一页没有专辑。{" "}
              <Link to="/artists/$artistId" params={{ artistId }} search={(previous) => ({ ...previous, [pageKey]: undefined })}>
                回到第一页
              </Link>
            </p>
          ) : (
            <div className={styles.results} aria-busy={query.isPlaceholderData}>
              <AlbumGridLayout>
                {page.items.map((item: ArtistAlbumDTO) => (
                  <ArtistAlbumCard key={item.group.id} item={item} meta={metaOf(item)} />
                ))}
              </AlbumGridLayout>
              <Pagination
                label={`${title}分页`}
                page={page.pagination.page}
                pageCount={pageCountOf(page.pagination.total, page.pagination.page_size)}
                renderLink={(target, props) => <Link to="/artists/$artistId" params={{ artistId }} search={(previous) => ({ ...previous, [pageKey]: target > 1 ? target : undefined })} onClick={showTop} {...props} />}
              />
            </div>
          )
        }
      </QueryContent>
    </Panel>
  );
}

/** 作为专辑艺人的专辑：没有时不出现。 */
function AlbumsSection({ artistId, search }: { artistId: string; search: ArtistDetailSearch }) {
  const albums = useArtistAlbums(artistId, search.albums_page ?? 1);
  return <AlbumShelf artistId={artistId} title="专辑" pageKey="albums_page" query={albums} metaOf={(item) => albumMeta(item.group.representative)} />;
}

/** 以其他身份参与（曲目艺人、作曲、演奏……）、但不是专辑艺人的专辑：没有时不出现。与专辑栏合起来就是页头的专辑数。 */
function AppearsOnSection({ artistId, search }: { artistId: string; search: ArtistDetailSearch }) {
  const albums = useArtistAppearsOnAlbums(artistId, search.appears_page ?? 1);
  return <AlbumShelf artistId={artistId} title="参与的专辑" pageKey="appears_page" query={albums} metaOf={(item) => albumArtistOf(item.group.representative)} />;
}

/** 有曲目的角色（按固定顺序）；地址里的角色不在其中时选第一个。 */
function trackRolesOf(detail: ArtistDetailDTO): ArtistTrackRole[] {
  return artistTrackRoles.filter((role) => detail.role_counts.some((count) => count.role === role && count.total > 0));
}

type TracksSectionProps = Pick<ArtistDetailViewProps, "renderTrackActions" | "renderTrackRowsScope"> & { detail: ArtistDetailDTO; search: ArtistDetailSearch };

/**
 * 参与的曲目：按角色分栏（链接，选中的带 aria-current），每栏各自翻页；含文件缺失的曲目（灰色、标“文件缺失”、不能播放，仍可收藏）。
 * 没有任何曲目级参与时不出现。
 */
function TracksSection({ detail, search, renderTrackActions, renderTrackRowsScope }: TracksSectionProps) {
  const roles = trackRolesOf(detail);
  const role = roles.find((candidate) => candidate === search.track_role) ?? roles[0];
  if (role === undefined) return null;
  return <RoleTracks key={role} detail={detail} roles={roles} role={role} search={search} renderTrackActions={renderTrackActions} renderTrackRowsScope={renderTrackRowsScope} />;
}

function RoleTracks({ detail, roles, role, search, renderTrackActions, renderTrackRowsScope }: TracksSectionProps & { roles: readonly ArtistTrackRole[]; role: ArtistTrackRole }) {
  const page = search.track_role === role ? (search.tracks_page ?? 1) : 1;
  const tracks = useArtistTracks(detail.artist.id, role, page);
  const { headingRef, showTop } = useSectionTop();
  const columns = useMemo(
    (): Column<TrackSummaryDTO>[] => [
      {
        id: "track",
        header: "曲目",
        rowHeader: true,
        truncate: true,
        width: "40%",
        cellTitle: (item) => trackTitleOf(item),
        cell: (item) => (
          <span className={item.present ? undefined : styles.missing}>
            <span className={styles.trackTitle}>{trackTitleOf(item)}</span>
            {item.present ? null : (
              <span className={styles.tag}>
                <StatusTag tone="outline">文件缺失</StatusTag>
              </span>
            )}
          </span>
        ),
      },
      {
        id: "artist",
        header: "艺人",
        truncate: true,
        width: "22%",
        secondary: true,
        cellTitle: (item) => trackArtistOf(item),
        cell: (item) => <ArtistNames text={trackArtistOf(item)} refs={trackArtistRefsOf(item)} />,
      },
      {
        id: "album",
        header: "专辑",
        truncate: true,
        width: "26%",
        secondary: true,
        cellTitle: (item) => item.release.title,
        cell: (item) =>
          item.present ? (
            <Link to="/albums/$groupId" params={{ groupId: item.group_id }} search={{ release: item.release.id }}>
              {item.release.title}
            </Link>
          ) : (
            <span className={styles.missing}>{item.release.title}</span>
          ),
      },
      { id: "actions", header: "操作", headerContent: <VisuallyHidden>操作</VisuallyHidden>, hideLabelWhenStacked: true, besideRowHeader: true, cell: (item) => renderTrackActions(item) },
      { id: "duration", header: "时长", numeric: true, secondary: true, cell: (item) => <span className={styles.duration}>{formatDuration(item.duration_seconds)}</span> },
    ],
    [renderTrackActions],
  );
  const total = detail.role_counts.find((count) => count.role === role)?.total ?? 0;
  return (
    <Panel title="参与的曲目" headingRef={headingRef} id="artist-tracks">
      {roles.length > 1 ? (
        <nav aria-label="按角色">
          <ul className={styles.roles}>
            {roles.map((candidate) => (
              <li key={candidate}>
                <Link
                  to="/artists/$artistId" params={{ artistId: detail.artist.id }}
                  search={(previous) => ({ ...previous, track_role: candidate, tracks_page: undefined })}
                  className={styles.role}
                  aria-current={candidate === role ? "page" : undefined}
                >
                  {artistRoleLabels[candidate]}
                  <span className={styles.roleCount}>{formatCount(detail.role_counts.find((count) => count.role === candidate)?.total ?? 0)}</span>
                </Link>
              </li>
            ))}
          </ul>
        </nav>
      ) : (
        <p className={styles.single}>
          {artistRoleLabels[role]} · {formatCount(total)} 首
        </p>
      )}
      <QueryContent query={tracks} loading={<Skeleton label="正在加载曲目" lines={4} />}>
        {(data) => {
          if (data.items.length === 0) {
            return data.pagination.total === 0 ? (
              <p>没有曲目。</p>
            ) : (
              <p>
                这一页没有曲目。{" "}
                <Link to="/artists/$artistId" params={{ artistId: detail.artist.id }} search={(previous) => ({ ...previous, tracks_page: undefined })}>
                  回到第一页
                </Link>
              </p>
            );
          }
          const table = <DataTable caption={`${artistRoleLabels[role]}的曲目`} columns={columns} rows={data.items} rowKey={(item) => item.track_id} contextRows />;
          return (
            <div className={styles.results} aria-busy={tracks.isPlaceholderData}>
              {renderTrackRowsScope === undefined ? table : renderTrackRowsScope(data.items, table)}
              <Pagination
                label="曲目分页"
                page={data.pagination.page}
                pageCount={pageCountOf(data.pagination.total, data.pagination.page_size)}
                renderLink={(target, props) => <Link to="/artists/$artistId" params={{ artistId: detail.artist.id }} search={(previous) => ({ ...previous, track_role: role, tracks_page: target > 1 ? target : undefined })} onClick={showTop} {...props} />}
              />
            </div>
          );
        }}
      </QueryContent>
    </Panel>
  );
}
