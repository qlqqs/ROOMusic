import { Link } from "@tanstack/react-router";
import { useEffect, useEffectEvent, useMemo, useRef, useState, type ReactNode } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { formatDuration } from "../../../shared/lib/duration";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { Button, DataTable, Page, Pagination, Panel, QueryContent, Skeleton, StatusTag, type Column } from "../../../shared/ui";
import {
  albumMeta,
  AlbumGridLayout,
  AlbumGridSkeleton,
  ArtistNames,
  ArtistMeta,
  artistRoleLabels,
  Cover,
  trackArtistOf,
  trackArtistRefsOf,
  trackTitleOf,
  type TrackSummaryDTO,
} from "../../catalog";
import { artistTrackRoles, type ArtistAlbumDTO, type ArtistDetailDTO, type ArtistTrackRole } from "../api/decoders";
import type { ArtistDetailSearch } from "../artist-search";
import { isArtistCatalogPreparing, useArtist, useArtistAlbums, useArtistNames, useArtistTracks } from "../hooks/use-artists";
import { ArtistPreparing } from "./artist-preparing";
import styles from "./artist-detail-view.module.css";

type ArtistDetailViewProps = {
  /** 路径里的艺人 ID（路由已校验为 UUID 并转成小写）；不是 UUID 时为 null，直接显示“艺人不存在”。 */
  artistId: string | null;
  search: ArtistDetailSearch;
  /** 这位艺人已经被合并进别的艺人：由 app 用替换历史的方式带到现在的艺人页。 */
  onRedirect: (artistId: string) => void;
  /** 头部的动作（播放全部、随机播放、心形）：由 app 组合。 */
  renderHeaderActions: (detail: ArtistDetailDTO) => ReactNode;
  /** 管理员的动作（合并、拆开、改名）：由 app 按角色传入；后端仍独立鉴权。 */
  renderAdminActions?: ((detail: ArtistDetailDTO) => ReactNode) | undefined;
  /** 曲目一行的动作（播放、心形、“更多”）：由 app 组合；传入稳定的函数引用。 */
  renderTrackActions: (item: TrackSummaryDTO) => ReactNode;
  /** 包住一页曲目行的范围（这一页的收藏状态一次读出）：由 app 提供；传入稳定的函数引用。 */
  renderTrackRowsScope?: ((items: readonly TrackSummaryDTO[], children: ReactNode) => ReactNode) | undefined;
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
 * 艺人页：主名、其他名字、头部的播放与心形、作为专辑艺人的专辑，以及按角色分栏的参与曲目（曲目艺人、作曲、指挥、演奏、制作人）。
 * 专辑、曲目与名字各自翻页，页码与选中的角色都在地址里。已经合并进别的艺人时跳到现在的艺人页；艺人数据还在整理时只说一句。
 */
export function ArtistDetailView({ artistId, search, onRedirect, renderHeaderActions, renderAdminActions, renderTrackActions, renderTrackRowsScope }: ArtistDetailViewProps) {
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
  const hasDescription = artist.album_count > 0 || artist.track_count > 0 || artist.roles.length > 0;
  return (
    <Page
      title={artist.name}
      compactTitle={artist.name.length > 80}
      {...(hasDescription ? { description: <ArtistMeta artist={artist} withRoles /> } : {})}
      actions={<div className={styles.headerActions}>{renderHeaderActions(data)}</div>}
    >
      {renderAdminActions === undefined ? null : renderAdminActions(data)}
      <NamesSection detail={data} search={search} />
      <AlbumsSection artistId={artist.id} search={search} />
      <TracksSection detail={data} search={search} renderTrackActions={renderTrackActions} renderTrackRowsScope={renderTrackRowsScope} />
    </Page>
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

/** 一张专辑：与专辑库同一种卡片（整张卡片是链接，卡片上的艺人名是文字）；打开时选中这位艺人署名的那个版本。 */
function ArtistAlbumCard({ item }: { item: ArtistAlbumDTO }) {
  const release = item.group.representative;
  const credited = item.credited_release_id === release.id ? undefined : item.credited_release_id;
  return (
    <li className={styles.album}>
      <Link to="/albums/$groupId" params={{ groupId: item.group.id }} search={{ release: credited }} className={styles.card}>
        <Cover artwork={release.artwork} title={release.title} variant="card" />
        <span className={styles.cardTitle}>{release.title}</span> <span className={styles.cardMeta}>{albumMeta(release)}</span>
        {item.group.release_count > 1 ? (
          <span className={styles.cardTags}>
            <StatusTag>{item.group.release_count} 个版本</StatusTag>
          </span>
        ) : null}
      </Link>
    </li>
  );
}

/** 作为专辑艺人的专辑：没有时不出现。 */
function AlbumsSection({ artistId, search }: { artistId: string; search: ArtistDetailSearch }) {
  const albums = useArtistAlbums(artistId, search.albums_page ?? 1);
  const { headingRef, showTop } = useSectionTop();
  const data = albums.data;
  if (data !== undefined && data.pagination.total === 0 && data.pagination.page === 1) return null;
  return (
    <Panel title="专辑" headingRef={headingRef} action={data === undefined ? undefined : <span>{formatCount(data.pagination.total)} 张</span>}>
      <QueryContent query={albums} loading={<AlbumGridSkeleton count={6} />}>
        {(page) =>
          page.items.length === 0 ? (
            <p>
              这一页没有专辑。{" "}
              <Link to="/artists/$artistId" params={{ artistId }} search={(previous) => ({ ...previous, albums_page: undefined })}>
                回到第一页
              </Link>
            </p>
          ) : (
            <div className={styles.results} aria-busy={albums.isPlaceholderData}>
              <AlbumGridLayout>
                {page.items.map((item) => (
                  <ArtistAlbumCard key={item.group.id} item={item} />
                ))}
              </AlbumGridLayout>
              <Pagination
                label="专辑分页"
                page={page.pagination.page}
                pageCount={pageCountOf(page.pagination.total, page.pagination.page_size)}
                renderLink={(target, props) => <Link to="/artists/$artistId" params={{ artistId }} search={(previous) => ({ ...previous, albums_page: target > 1 ? target : undefined })} onClick={showTop} {...props} />}
              />
            </div>
          )
        }
      </QueryContent>
    </Panel>
  );
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
        cell: (item) => (
          <div className={item.present ? undefined : styles.missing}>
            <span className={styles.trackTitle}>{trackTitleOf(item)}</span>
            <span className={styles.trackArtist}>
              <ArtistNames text={trackArtistOf(item)} refs={trackArtistRefsOf(item)} />
            </span>
            {item.present ? null : (
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
        cell: (item) =>
          item.present ? (
            <Link to="/albums/$groupId" params={{ groupId: item.group_id }} search={{ release: item.release.id }}>
              {item.release.title}
            </Link>
          ) : (
            <span className={styles.missing}>{item.release.title}</span>
          ),
      },
      { id: "duration", header: "时长", numeric: true, cell: (item) => <span className={styles.duration}>{formatDuration(item.duration_seconds)}</span> },
      { id: "actions", header: "操作", hideLabelWhenStacked: true, cell: (item) => renderTrackActions(item) },
    ],
    [renderTrackActions],
  );
  const total = detail.role_counts.find((count) => count.role === role)?.total ?? 0;
  return (
    <Panel title="参与的曲目" headingRef={headingRef}>
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
          const table = <DataTable caption={`${artistRoleLabels[role]}的曲目`} columns={columns} rows={data.items} rowKey={(item) => item.track_id} />;
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
