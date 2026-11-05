import { useRef, type ReactNode } from "react";
import { formatCount } from "../../../shared/lib/number";
import { Panel, QueryContent, Skeleton, type PaginationLinkProps, type QuerySource } from "../../../shared/ui";
import type { SearchPaginationDTO } from "../api/decoders";
import { SearchPagination } from "./search-pagination";
import styles from "./result-section.module.css";

type ResultPage<T> = { items: readonly T[]; pagination: SearchPaginationDTO };

type ResultSectionProps<T> = {
  title: string;
  /** 计数的量词：“张”“首”。 */
  unit: string;
  query: QuerySource<ResultPage<T>> & { isPlaceholderData: boolean };
  emptyText: string;
  /** 指向这一栏某一页的链接（只改这一栏的页码，另一栏不动）。 */
  renderPageLink: (page: number, props: PaginationLinkProps & { onClick: () => void }) => ReactNode;
  children: (items: readonly T[]) => ReactNode;
};

/** 结果数：不超过 1,000 时是精确数，超过时显示“1,000+”（接口只探测到第 1,001 个就停）。 */
export function resultCountText(pagination: Pick<SearchPaginationDTO, "total" | "total_relation">, unit: string): string {
  return `${formatCount(pagination.total)}${pagination.total_relation === "gte" ? "+" : ""} ${unit}`;
}

/**
 * 结果页的一栏：标题与结果数、首次加载、失败与重试（只影响这一栏）、没有结果、页码超出（给回到第一页的链接）与分页。
 * 翻页时旧结果保持可见并标 aria-busy；点页码链接时把这一栏的标题滚到视野顶部并聚焦。
 */
export function ResultSection<T>({ title, unit, query, emptyText, renderPageLink, children }: ResultSectionProps<T>) {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const showTop = () => {
    const heading = headingRef.current;
    if (heading === null) return;
    heading.scrollIntoView({ block: "start" });
    heading.focus({ preventScroll: true });
  };
  const data = query.data;
  return (
    <Panel
      title={title}
      headingRef={headingRef}
      action={
        <p className={styles.count} aria-live="polite">
          {data === undefined || data.pagination.total === 0 ? "" : resultCountText(data.pagination, unit)}
        </p>
      }
    >
      <QueryContent query={query} loading={<Skeleton label={`正在查找${title}`} lines={3} />}>
        {({ items, pagination }) => {
          if (pagination.total === 0 && items.length === 0 && pagination.page === 1) return <p className={styles.empty}>{emptyText}</p>;
          return (
            <div className={styles.results} aria-busy={query.isPlaceholderData}>
              {items.length === 0 ? (
                <p className={styles.empty}>
                  这一页没有结果。 {renderPageLink(1, { className: "", rel: "prev", children: "回到第一页", onClick: showTop })}
                </p>
              ) : (
                children(items)
              )}
              <SearchPagination
                label={`${title}结果分页`}
                page={pagination.page}
                hasMore={pagination.has_more}
                renderLink={(target, props) => renderPageLink(target, { ...props, onClick: showTop })}
              />
            </div>
          );
        }}
      </QueryContent>
    </Panel>
  );
}
