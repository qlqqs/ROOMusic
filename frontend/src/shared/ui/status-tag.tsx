import type { ReactNode } from "react";
import styles from "./status-tag.module.css";

type StatusTagProps = {
  /** 色调只区分层级；状态含义由文字和图标承担，金色不等于成功或风险等级。 */
  tone?: "neutral" | "accent" | "outline";
  icon?: ReactNode;
  children: ReactNode;
};

export function StatusTag({ tone = "neutral", icon, children }: StatusTagProps) {
  return (
    <span className={styles.tag} data-tone={tone}>
      {icon === undefined ? null : (
        <span className={styles.icon} aria-hidden="true">
          {icon}
        </span>
      )}
      {children}
    </span>
  );
}
