import type { ReactNode } from "react";
import { cx } from "../lib/class-names";
import styles from "./pagination.module.css";

export type PaginationLinkProps = {
  className: string;
  /** 上一页或下一页：写进链接的 rel，表达页与页之间的关系。 */
  rel: "prev" | "next";
  children: ReactNode;
};

type PaginationProps = {
  /** 导航的可访问名称，例如“扫描记录分页”：同一页面可能有不止一个分页。 */
  label: string;
  /** 当前页，从 1 起。 */
  page: number;
  /** 总页数。只有一页（或没有数据）时不渲染。 */
  pageCount: number;
  /** 由调用方生成指向某一页的链接（通常是路由 Link）：组件只负责结构与可访问语义，不依赖路由。 */
  renderLink: (page: number, props: PaginationLinkProps) => ReactNode;
};

/**
 * 分页导航：“上一页”“第 x / y 页”“下一页”。当前页用 aria-current="page" 标出；
 * 到头时对应的方向显示为不可用的文字而不是消失，布局不跳动。
 */
export function Pagination({ label, page, pageCount, renderLink }: PaginationProps) {
  if (pageCount <= 1) return null;
  return (
    <nav aria-label={label} className={styles.pagination}>
      <ul className={styles.list}>
        <li>
          {page > 1 ? (
            renderLink(Math.min(page - 1, pageCount), { className: cx(styles.link), rel: "prev", children: "上一页" })
          ) : (
            <span className={styles.unavailable} aria-disabled="true">
              上一页
            </span>
          )}
        </li>
        <li>
          <span className={styles.current} aria-current="page">
            第 {page} / {pageCount} 页
          </span>
        </li>
        <li>
          {page < pageCount ? (
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
