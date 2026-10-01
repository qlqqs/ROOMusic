import type { ReactNode } from "react";
import styles from "./stat-list.module.css";

export type Stat = { label: string; value: ReactNode };

/** 一组“名称：数值”，用描述列表表达，数字使用等宽数字特性。 */
export function StatList({ items }: { items: readonly Stat[] }) {
  return (
    <dl className={styles.list}>
      {items.map((item) => (
        <div key={item.label} className={styles.item}>
          <dt className={styles.label}>{item.label}</dt>
          <dd className={styles.value}>{item.value}</dd>
        </div>
      ))}
    </dl>
  );
}
