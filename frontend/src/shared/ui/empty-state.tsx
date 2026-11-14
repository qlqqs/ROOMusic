import type { ReactNode } from "react";
import styles from "./empty-state.module.css";

type EmptyStateProps = {
  /** 装饰性的图标（对读屏隐藏）。 */
  icon: ReactNode;
  /** 一句话说明现在是什么情况，例如“还没有歌单”。 */
  title: string;
  /** 下一步怎么做。 */
  children: ReactNode;
  /** 下一步的入口（按钮或链接）；没有时不渲染。 */
  action?: ReactNode;
};

/** 列表为空时的居中说明：图标、标题、一两句下一步与可选的入口，虚线框围起来，与“加载中”“失败”区分开。 */
export function EmptyState({ icon, title, children, action }: EmptyStateProps) {
  return (
    <div className={styles.empty}>
      <span className={styles.icon} aria-hidden="true">
        {icon}
      </span>
      <p className={styles.title}>{title}</p>
      <div className={styles.text}>{children}</div>
      {action === undefined ? null : <div className={styles.action}>{action}</div>}
    </div>
  );
}
