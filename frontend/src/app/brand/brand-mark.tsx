import { cx } from "../../shared/lib/class-names";
import logoUrl from "./logo-small.svg";
import styles from "./brand-mark.module.css";

/** 品牌标志：主图标加 "ROOMusic" 文字。图标只是装饰，大小随所在位置的字号变化。 */
export function BrandMark({ className }: { className?: string }) {
  return (
    <span className={cx(styles.mark, className)}>
      <img src={logoUrl} alt="" aria-hidden="true" className={styles.icon} />
      ROOMusic
    </span>
  );
}
