import type { ReactNode } from "react";
import { useId } from "react";
import styles from "./panel.module.css";

type PanelProps = {
  title: string;
  /** 标题右侧的操作，例如跳转链接。 */
  action?: ReactNode;
  children: ReactNode;
};

/** 页面里的一个区块：细线分隔、h2 标题，不做装饰性卡片。 */
export function Panel({ title, action, children }: PanelProps) {
  const headingId = useId();
  return (
    <section className={styles.panel} aria-labelledby={headingId}>
      <div className={styles.header}>
        <h2 id={headingId}>{title}</h2>
        {action === undefined ? null : <div className={styles.action}>{action}</div>}
      </div>
      <div className={styles.body}>{children}</div>
    </section>
  );
}
