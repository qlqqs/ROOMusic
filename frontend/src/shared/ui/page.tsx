import type { ReactNode } from "react";
import { usePageTitle } from "../lib/use-page-title";
import styles from "./page.module.css";

type PageProps = {
  /** 页面的唯一 h1，同时是文档标题。 */
  title: string;
  /** 标题很长（例如超过 80 个字符的专辑名）时降一级字号：完整显示并换行，不侵占操作区。 */
  compactTitle?: boolean;
  description?: string;
  /** 页头右侧的主要操作。 */
  actions?: ReactNode;
  children: ReactNode;
};

/** 已登录页面的统一结构：一个 h1、可选说明与操作，然后是各区块。 */
export function Page({ title, compactTitle = false, description, actions, children }: PageProps) {
  usePageTitle(title);
  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div className={styles.heading} data-compact={compactTitle ? "" : undefined}>
          <h1>{title}</h1>
          {description === undefined ? null : <p className={styles.description}>{description}</p>}
        </div>
        {actions === undefined ? null : <div className={styles.actions}>{actions}</div>}
      </header>
      {children}
    </div>
  );
}
