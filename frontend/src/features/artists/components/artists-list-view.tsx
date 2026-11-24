import { Link, useNavigate } from "@tanstack/react-router";
import { useId, useRef, type ReactNode } from "react";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Pagination, Panel, QueryContent, Skeleton, VisuallyHidden } from "../../../shared/ui";
import { artistInitialGroups, artistRoleLabels, artistRolesText, initialGroupLabel, type ArtistInitialGroup, type ArtistSummaryDTO } from "../../catalog";
import type { ArtistsSearch } from "../artist-search";
import { ARTISTS_PAGE_SIZE, isArtistCatalogPreparing, useArtistsPage } from "../hooks/use-artists";
import { ArtistFilters } from "./artist-filters";
import { ArtistMonogram } from "./artist-monogram";
import { ArtistPreparing } from "./artist-preparing";
import styles from "./artists-list-view.module.css";

function resultsTitle(search: ArtistsSearch): string {
  if (search.q !== undefined) return `“${search.q}”的筛选结果`;
  if (search.role !== undefined) return `担任${artistRoleLabels[search.role]}的艺人`;
  return "全部艺人";
}

/** 首字母导航里的短写：竖排索引（手机）放不下“全部”“假名”。 */
function shortInitialLabel(group: ArtistInitialGroup | null): string {
  if (group === null) return "全";
  return group === "kana" ? "あ" : group;
}

/**
 * 首字母导航：只在按名字排序时出现；“全部”与每个分组都是链接（按首字母筛选），当前的反白并带 aria-current。
 * 宽屏是一条紧凑的横排索引，滚动时停在主内容区顶部；手机上是贴在右侧的竖排索引。各字母有没有艺人接口没有给出，不做变灰。
 */
function InitialNav({ search }: { search: ArtistsSearch }) {
  const items: readonly (ArtistInitialGroup | null)[] = [null, ...artistInitialGroups];
  return (
    <nav aria-label="首字母" className={styles.initials}>
      <ul className={styles.initialList}>
        {items.map((group) => (
          <li key={group ?? "all"}>
            <Link
              to="/artists"
              search={{ ...search, initial: group ?? undefined, page: undefined }}
              className={styles.initial}
              data-wide={group === null || group === "kana" ? "" : undefined}
              aria-current={search.initial === (group ?? undefined) ? "page" : undefined}
              activeOptions={{ exact: true, includeSearch: true }}
            >
              <span className={styles.longLabel}>{group === null ? "全部" : initialGroupLabel(group)}</span>
              <span className={styles.shortLabel} aria-hidden="true">
                {shortInitialLabel(group)}
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  );
}

type RenderArtistPlay = ((artist: ArtistSummaryDTO) => ReactNode) | undefined;

/**
 * 一位艺人：整行是一个链接——圆章（名字缩写）、名字、参与的角色（“作曲、指挥”）与右侧的专辑数；没有下划线，悬停有浅底。
 * 给了 renderPlay 时链接之后多一个播放键，悬停或聚焦时盖在圆章上（精确指针设备；触屏不显示）。
 */
function ArtistItem({ artist, renderPlay }: { artist: ArtistSummaryDTO; renderPlay: RenderArtistPlay }) {
  const id = useId();
  const roles = artistRolesText(artist.roles);
  const hasRoles = roles !== "";
  const hasCount = artist.album_count > 0;
  // 链接名称只是名字，角色与专辑数作为它的说明：读屏的链接列表里一眼能找到人。
  const described = [hasRoles ? `${id}-roles` : null, hasCount ? `${id}-count` : null].filter((part) => part !== null).join(" ");
  return (
    <li className={styles.item}>
      <Link to="/artists/$artistId" params={{ artistId: artist.id }} className={styles.row} aria-labelledby={`${id}-name`} aria-describedby={described === "" ? undefined : described}>
        <ArtistMonogram id={artist.id} name={artist.name} image={artist.image} />
        <span className={styles.text}>
          <span id={`${id}-name`} className={styles.name}>
            {artist.name}
          </span>
          {hasRoles ? (
            <span id={`${id}-roles`} className={styles.roles}>
              {roles}
            </span>
          ) : null}
        </span>
        {hasCount ? (
          <span id={`${id}-count`} className={styles.count}>
            {formatCount(artist.album_count)}
            <VisuallyHidden> 张专辑</VisuallyHidden>
          </span>
        ) : null}
      </Link>
      {renderPlay === undefined ? null : <div className={styles.play}>{renderPlay(artist)}</div>}
    </li>
  );
}

/** 按名字排序时按首字母分组（接口已按分组排好），每组一个衬线大字的分隔标题，像唱片目录的分隔卡；按专辑数排序时不分组。 */
function ArtistGroups({ items, grouped, renderPlay }: { items: readonly ArtistSummaryDTO[]; grouped: boolean; renderPlay: RenderArtistPlay }) {
  if (!grouped) {
    return (
      <ul className={styles.list}>
        {items.map((artist) => (
          <ArtistItem key={artist.id} artist={artist} renderPlay={renderPlay} />
        ))}
      </ul>
    );
  }
  const groups: { group: ArtistInitialGroup; artists: ArtistSummaryDTO[] }[] = [];
  for (const artist of items) {
    const last = groups.at(-1);
    if (last !== undefined && last.group === artist.initial_group) last.artists.push(artist);
    else groups.push({ group: artist.initial_group, artists: [artist] });
  }
  return (
    <div className={styles.groups}>
      {groups.map(({ group, artists }) => (
        <section key={group} aria-label={initialGroupLabel(group)} className={styles.group}>
          <h3 className={styles.groupHeading}>{initialGroupLabel(group)}</h3>
          <ul className={styles.list}>
            {artists.map((artist) => (
              <ArtistItem key={artist.id} artist={artist} renderPlay={renderPlay} />
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

/**
 * 艺人列表：筛选、角色、两种排序、首字母与分页都在地址里；每行的播放键经 renderArtistPlay 由 app 组合（artists 不导入 player）。按名字排序时按首字母分组，按专辑数排序时不分组。
 * 页码越界时用替换历史的方式改到末页；翻页时旧结果保持可见并标 aria-busy。艺人数据还在整理时只说一句。
 */
export function ArtistsListView({ search, renderArtistPlay }: { search: ArtistsSearch; renderArtistPlay?: RenderArtistPlay }) {
  const sort = search.sort ?? "name";
  const query = useArtistsPage({ q: search.q, initial: search.initial, role: search.role, sort, page: search.page ?? 1, pageSize: ARTISTS_PAGE_SIZE });
  const navigate = useNavigate();
  const headingRef = useRef<HTMLHeadingElement>(null);
  const showTop = () => {
    const heading = headingRef.current;
    if (heading === null) return;
    heading.scrollIntoView({ block: "start" });
    heading.focus({ preventScroll: true });
  };
  const pagination = query.data?.pagination;
  const outOfRange = useClampedPage({
    page: search.page ?? 1,
    pageCount: pagination === undefined ? 0 : pageCountOf(pagination.total, pagination.page_size),
    ready: pagination !== undefined && !query.isPlaceholderData,
    onOutOfRange: (lastPage) => void navigate({ to: "/artists", search: { ...search, page: lastPage > 1 ? lastPage : undefined }, replace: true }),
  });
  if (query.isError && query.data === undefined && isArtistCatalogPreparing(query.error)) return <ArtistPreparing />;
  const filtered = search.q !== undefined || search.role !== undefined || search.initial !== undefined;
  return (
    <>
      <ArtistFilters search={search} />
      {sort === "name" ? <InitialNav search={search} /> : null}
      {/* 手机上首字母是贴在右侧的竖排索引：结果区右侧给它留出位置。 */}
      <div className={styles.resultsArea} data-rail={sort === "name" ? "" : undefined}>
        <Panel
          title={resultsTitle(search)}
          headingRef={headingRef}
          action={pagination === undefined ? undefined : <span aria-live="polite">{formatCount(pagination.total)} 位艺人</span>}
        >
          <QueryContent query={query} loading={<Skeleton label="正在加载艺人" lines={6} />}>
            {(data) => {
              if (outOfRange) return <Skeleton label="正在加载艺人" lines={6} />;
              if (data.pagination.total === 0) {
                return filtered ? (
                  <p>
                    没有符合条件的艺人。{" "}
                    <Link to="/artists" search={{ sort: search.sort }}>
                      清除筛选
                    </Link>
                  </p>
                ) : (
                  <p>资料库里还没有艺人。</p>
                );
              }
              return (
                <div className={styles.results} aria-busy={query.isPlaceholderData}>
                  <ArtistGroups items={data.items} grouped={sort === "name"} renderPlay={renderArtistPlay} />
                  <Pagination
                    label="艺人分页"
                    page={data.pagination.page}
                    pageCount={pageCountOf(data.pagination.total, data.pagination.page_size)}
                    renderLink={(target, props) => <Link to="/artists" search={{ ...search, page: target > 1 ? target : undefined }} onClick={showTop} {...props} />}
                  />
                </div>
              );
            }}
          </QueryContent>
        </Panel>
      </div>
    </>
  );
}
