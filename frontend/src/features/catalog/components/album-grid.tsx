import type { ReactNode } from "react";
import { VisuallyHidden } from "../../../shared/ui";
import type { ReleaseGroupSummaryDTO } from "../api/decoders";
import type { AlbumSearch } from "../album-search";
import { AlbumCard } from "./album-card";
import styles from "./album-grid.module.css";

/** 卡片网格：列数随容器宽度变化（每列至少 160px），每页最多 48 张。 */
export function AlbumGrid({ groups, search }: { groups: readonly ReleaseGroupSummaryDTO[]; search: AlbumSearch }) {
  return (
    <ul className={styles.grid}>
      {groups.map((group) => (
        <AlbumCard key={group.id} group={group} search={search} />
      ))}
    </ul>
  );
}

/** 与专辑卡片同一种网格（每列至少 160px）：别的功能自己的卡片（例如收藏的专辑）放进来，与专辑库的版面一致。子项是 li。 */
export function AlbumGridLayout({ children }: { children: ReactNode }) {
  return <ul className={styles.grid}>{children}</ul>;
}

const PLACEHOLDER_CARDS = 12;

/** 首次加载的占位：与卡片网格同结构（方形封面加两行文字），不画成空库，也没有动画。count 是占位卡片数（默认 12）。 */
export function AlbumGridSkeleton({ count = PLACEHOLDER_CARDS }: { count?: number }) {
  return (
    <div role="status">
      <VisuallyHidden>正在加载专辑</VisuallyHidden>
      <ul className={styles.grid} aria-hidden="true">
        {Array.from({ length: count }, (_, index) => (
          <li key={index} className={styles.placeholder}>
            <span className={styles.placeholderCover} />
            <span className={styles.placeholderLine} />
            <span className={styles.placeholderLine} />
          </li>
        ))}
      </ul>
    </div>
  );
}
