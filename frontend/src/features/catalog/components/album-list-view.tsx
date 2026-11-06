import { Link, useNavigate } from "@tanstack/react-router";
import { useRef } from "react";
import { pageCountOf } from "../../../shared/lib/pagination";
import { formatCount } from "../../../shared/lib/number";
import { useClampedPage } from "../../../shared/lib/use-clamped-page";
import { Pagination, Panel, QueryContent } from "../../../shared/ui";
import { isFiltered, type AlbumSearch } from "../album-search";
import { useAlbumPage } from "../hooks/use-album-page";
import { AlbumFilters } from "./album-filters";
import { AlbumGrid, AlbumGridSkeleton } from "./album-grid";
import styles from "./album-list-view.module.css";

/** 结果区块的标题：说明现在看到的是哪一组专辑。 */
function resultsTitle(search: AlbumSearch): string {
  if (search.q !== undefined && search.attention !== undefined) return `“${search.q}”中需要关注的专辑`;
  if (search.q !== undefined) return `“${search.q}”的搜索结果`;
  if (search.attention !== undefined) return "需要关注的专辑";
  return "全部专辑";
}

/**
 * 专辑列表：搜索、筛选与排序、卡片网格、分页。搜索词、筛选、排序与页码都在地址里（可刷新、可前进后退）；页码越界时用替换历史的方式改到末页。
 * 翻页与改筛选时旧结果保持可见并标明“正在更新”。isAdmin 只决定空库时的说明，后端仍独立鉴权。
 */
export function AlbumListView({ search, isAdmin }: { search: AlbumSearch; isAdmin: boolean }) {
  const query = useAlbumPage(search);
  const navigate = useNavigate();
  const resultsHeading = useRef<HTMLHeadingElement>(null);
  // 页码链接在结果的下方：不挪动的话，新的一页会停在滚到底的位置，键盘用户还要倒着穿过整页卡片才能到第一张。
  // 点页码链接时把结果顶部滚到视野里并聚焦结果标题，接着 Tab 就是第一张卡片。
  const showResultsTop = () => {
    const heading = resultsHeading.current;
    if (heading === null) return;
    heading.scrollIntoView({ block: "start" });
    heading.focus({ preventScroll: true });
  };
  const pagination = query.data?.pagination;
  const outOfRange = useClampedPage({
    page: search.page ?? 1,
    pageCount: pagination === undefined ? 0 : pageCountOf(pagination.total, pagination.page_size),
    ready: pagination !== undefined && !query.isPlaceholderData,
    onOutOfRange: (lastPage) => void navigate({ to: "/albums", search: { ...search, page: lastPage > 1 ? lastPage : undefined }, replace: true }),
  });
  const filtered = isFiltered(search);

  return (
    <>
      <AlbumFilters search={search} />
      <Panel
        title={resultsTitle(search)}
        headingRef={resultsHeading}
        action={
          pagination === undefined ? undefined : (
            <span className={styles.count}>
              {/* 读屏只在结果到达、数量变化时播报一次；“正在更新”只给看得见的人，不制造多余的播报。 */}
              <span aria-live="polite">{filtered ? "找到" : "共"} {formatCount(pagination.total)} 张专辑</span>
              {query.isPlaceholderData ? <span aria-hidden="true">，正在更新…</span> : null}
            </span>
          )
        }
      >
        <QueryContent query={query} loading={<AlbumGridSkeleton />}>
          {(data) => {
            if (outOfRange) return <AlbumGridSkeleton />;
            if (data.pagination.total === 0) {
              return filtered ? (
                <p>
                  没有符合条件的专辑。{" "}
                  <Link to="/albums" search={{ sort: search.sort }}>
                    清除筛选
                  </Link>
                </p>
              ) : isAdmin ? (
                <p>
                  资料库里还没有专辑。添加音乐目录并扫描后就会出现。 <Link to="/settings/library">前往扫描目录</Link>
                </p>
              ) : (
                <p>资料库里还没有专辑。</p>
              );
            }
            return (
              <div className={styles.results} aria-busy={query.isPlaceholderData}>
                <AlbumGrid groups={data.items} search={search} />
                <Pagination
                  label="专辑分页"
                  page={data.pagination.page}
                  pageCount={pageCountOf(data.pagination.total, data.pagination.page_size)}
                  renderLink={(target, props) => (
                    <Link
                      to="/albums"
                      search={{ q: search.q, attention: search.attention, sort: search.sort, page: target > 1 ? target : undefined }}
                      onClick={showResultsTop}
                      {...props}
                    />
                  )}
                />
              </div>
            );
          }}
        </QueryContent>
      </Panel>
    </>
  );
}
