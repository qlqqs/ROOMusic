import type { ReactNode } from "react";
import { BrandMark } from "../brand/brand-mark";
import { usePageTitle } from "../../shared/lib/use-page-title";
import styles from "./auth-layout.module.css";

type AuthLayoutProps = {
  /** 页面的唯一 h1，同时是文档标题。 */
  title: string;
  description?: string;
  children: ReactNode;
};

/** 登录、初始化与启动失败共用的居中纸面布局（不含已登录外壳）。 */
export function AuthLayout({ title, description, children }: AuthLayoutProps) {
  usePageTitle(title);
  return (
    <main className={styles.page}>
      <div className={styles.sheet}>
        <p className={styles.brand}><BrandMark /></p>
        <header className={styles.header}>
          <h1>{title}</h1>
          {description === undefined ? null : <p className={styles.description}>{description}</p>}
        </header>
        {children}
      </div>
    </main>
  );
}
