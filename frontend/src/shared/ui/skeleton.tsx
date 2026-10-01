import styles from "./skeleton.module.css";
import { VisuallyHidden } from "./visually-hidden";

type SkeletonProps = {
  /** 读屏播报的加载说明，例如“正在加载目录”。 */
  label: string;
  lines?: number;
};

/** 首次加载占位：保留区块结构，不画成空库，也不做会触发减少动态偏好的动画。 */
export function Skeleton({ label, lines = 3 }: SkeletonProps) {
  return (
    <div className={styles.skeleton} role="status">
      <VisuallyHidden>{label}</VisuallyHidden>
      {Array.from({ length: lines }, (_, index) => (
        <span key={index} className={styles.line} aria-hidden="true" />
      ))}
    </div>
  );
}
