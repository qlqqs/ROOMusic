import type { ReactNode } from "react";
import { cx } from "../../../shared/lib/class-names";
import type { PaginationLinkProps } from "../../../shared/ui";
import styles from "./search-pagination.module.css";

type SearchPaginationProps = {
  /** 导航的可访问名称：同一页面有两栏，各自一个分页。 */
  label: string;
  page: number;
  /** 还有下一页（来自接口的 has_more）；总数超过 1,000 时只是下界，不能据此算出末页。 */
  hasMore: boolean;
  renderLink: (page: number, props: PaginationLinkProps) => ReactNode;
};

/**
 * 搜索结果的分页：“上一页”“第 N 页”“下一页”，下一页由 has_more 决定，不写总页数（显示“1,000+”时末页未知）。
 * 只有一页时不渲染；到头的方向显示为不可用的文字，布局不跳动。
 */
export function SearchPagination({ label, page, hasMore, renderLink }: SearchPaginationProps) {
  if (page <= 1 && !hasMore) return null;
  return (
    <nav aria-label={label} className={styles.pagination}>
      <ul className={styles.list}>
        <li>
          {page > 1 ? (
            renderLink(page - 1, { className: cx(styles.link), rel: "prev", children: "上一页" })
          ) : (
            <span className={styles.unavailable} aria-disabled="true">
              上一页
            </span>
          )}
        </li>
        <li>
          <span className={styles.current} aria-current="page">
            第 {page} 页
          </span>
        </li>
        <li>
          {hasMore ? (
            renderLink(page + 1, { className: cx(styles.link), rel: "next", children: "下一页" })
          ) : (
            <span className={styles.unavailable} aria-disabled="true">
              下一页
            </span>
          )}
        </li>
      </ul>
    </nav>
  );
}
