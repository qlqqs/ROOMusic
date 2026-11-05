import { Link, useNavigate } from "@tanstack/react-router";
import { useRef } from "react";
import { formatCount } from "../../../shared/lib/number";
import { pageCountOf } from "../../../shared/lib/pagination";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Pagination, Panel, QueryContent, Skeleton } from "../../../shared/ui";
import { artistInitialGroups, artistMetaText, artistRoleLabels, artistRolesText, initialGroupLabel, type ArtistInitialGroup, type ArtistSummaryDTO } from "../../catalog";
import type { ArtistsSearch } from "../artist-search";
import { ARTISTS_PAGE_SIZE, isArtistCatalogPreparing, useArtistsPage } from "../hooks/use-artists";
import { ArtistFilters } from "./artist-filters";
import { ArtistPreparing } from "./artist-preparing";
import styles from "./artists-list-view.module.css";

function resultsTitle(search: ArtistsSearch): string {
  if (search.q !== undefined) return `“${search.q}”的查找结果`;
  if (search.role !== undefined) return `担任${artistRoleLabels[search.role]}的艺人`;
  return "全部艺人";
}

/** 首字母导航：只在按名字排序时出现；“全部”与每个分组都是链接，当前的带 aria-current。 */
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
              aria-current={search.initial === (group ?? undefined) ? "page" : undefined}
              activeOptions={{ exact: true, includeSearch: true }}
            >
              {group === null ? "全部" : initialGroupLabel(group)}
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  );
}

function ArtistItem({ artist }: { artist: ArtistSummaryDTO }) {
  const meta = artistMetaText(artist);
  const roles = artistRolesText(artist.roles);
  return (
    <li className={styles.item}>
      <Link to="/artists/$artistId" params={{ artistId: artist.id }} className={styles.name}>
        {artist.name}
      </Link>
      {meta === "" && roles === "" ? null : <span className={styles.meta}>{[meta, roles].filter((part) => part !== "").join(" · ")}</span>}
    </li>
  );
}

/** 按名字排序时按首字母分组（接口已按分组排好），每组一个小标题；按专辑数排序时不分组。 */
function ArtistGroups({ items, grouped }: { items: readonly ArtistSummaryDTO[]; grouped: boolean }) {
  if (!grouped) {
    return (
      <ul className={styles.list}>
        {items.map((artist) => (
          <ArtistItem key={artist.id} artist={artist} />
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
              <ArtistItem key={artist.id} artist={artist} />
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

/**
 * 艺人列表：查找、角色筛选、两种排序、首字母与分页都在地址里。按名字排序时按首字母分组，按专辑数排序时不分组。
 * 页码越界时用替换历史的方式改到末页；翻页时旧结果保持可见并标 aria-busy。艺人数据还在整理时只说一句。
 */
export function ArtistsListView({ search }: { search: ArtistsSearch }) {
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
                <ArtistGroups items={data.items} grouped={sort === "name"} />
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
    </>
  );
}
