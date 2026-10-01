import type { ReactNode } from "react";
import { usePageTitle } from "../lib/use-page-title";
import styles from "./page.module.css";

type PageProps = {
  /** 页面的唯一 h1，同时是文档标题。 */
  title: string;
  description?: string;
  /** 页头右侧的主要操作。 */
  actions?: ReactNode;
  children: ReactNode;
};

/** 已登录页面的统一结构：一个 h1、可选说明与操作，然后是各区块。 */
export function Page({ title, description, actions, children }: PageProps) {
  usePageTitle(title);
  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <div className={styles.heading}>
          <h1>{title}</h1>
          {description === undefined ? null : <p className={styles.description}>{description}</p>}
        </div>
        {actions === undefined ? null : <div className={styles.actions}>{actions}</div>}
      </header>
      {children}
    </div>
  );
}
