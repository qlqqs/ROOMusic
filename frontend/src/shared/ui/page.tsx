import type { ReactNode } from "react";
import { usePageTitle } from "../lib/use-page-title";
import styles from "./page.module.css";

type PageProps = {
  /** 页面的唯一 h1，同时是文档标题。 */
  title: string;
  /** 标题很长（例如超过 80 个字符的专辑名）时降一级字号：完整显示并换行，不侵占操作区。 */
  compactTitle?: boolean;
  /** 紧跟在标题右侧的小字（例如设置页正在作用的设备名）。 */
  titleAside?: ReactNode;
  /** 标题下的一行说明；可以含链接（例如专辑页的艺人名）。 */
  description?: ReactNode;
  /** 页头右侧的主要操作。 */
  actions?: ReactNode;
  /** 自定义页头（如歌单的封面头部），替换默认的标题、说明与操作；由它渲染唯一的 h1，文字与 title 相同。 */
  header?: ReactNode;
  children: ReactNode;
};

/** 已登录页面的统一结构：一个 h1、可选说明与操作，然后是各区块。 */
export function Page({ title, compactTitle = false, titleAside, description, actions, header, children }: PageProps) {
  usePageTitle(title);
  return (
    <div className={styles.page}>
      {header ?? (
        <header className={styles.header}>
          <div className={styles.heading} data-compact={compactTitle ? "" : undefined}>
            {titleAside === undefined ? <h1>{title}</h1> : <div className={styles.titleRow}><h1>{title}</h1>{titleAside}</div>}
            {description === undefined ? null : <p className={styles.description}>{description}</p>}
          </div>
          {actions === undefined ? null : <div className={styles.actions}>{actions}</div>}
        </header>
      )}
      {children}
    </div>
  );
}
