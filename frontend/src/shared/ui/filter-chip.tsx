import type { ReactNode } from "react";
import styles from "./filter-chip.module.css";

type FilterChipProps = {
  pressed: boolean;
  onClick: () => void;
  children: ReactNode;
};

/**
 * 列表工具条里的胶囊筛选（艺人的角色、专辑的“需要关注”）：一个带 aria-pressed 的按钮，选中时金色轻底加粗，不只靠颜色。
 * 一组互斥的胶囊由调用方放进带名称的 role="group"。
 */
export function FilterChip({ pressed, onClick, children }: FilterChipProps) {
  return (
    <button type="button" className={styles.chip} aria-pressed={pressed} onClick={onClick}>
      {children}
    </button>
  );
}
