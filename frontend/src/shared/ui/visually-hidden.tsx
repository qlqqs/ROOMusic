import type { ReactNode } from "react";
import styles from "./visually-hidden.module.css";

/** 只对读屏可见的文字：用来给纯图形或占位区域补充名称。 */
export function VisuallyHidden({ children }: { children: ReactNode }) {
  return <span className={styles.hidden}>{children}</span>;
}
