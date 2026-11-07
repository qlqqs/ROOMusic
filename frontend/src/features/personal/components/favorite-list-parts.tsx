import { useRef, useState } from "react";
import { failureOf, isHttpFailure } from "../../../shared/api";
import { Button } from "../../../shared/ui";
import styles from "./favorites-view.module.css";

// 收藏列表（我喜欢的音乐、我的收藏）共用的翻页版本、回到顶部与“列表有更新”。

/**
 * 翻页时带上的集合版本：只对目标页有效。服务端说集合变了（409 favorites_changed）就丢掉它，从当前版本重新读这一页；
 * 这一页里保留的行与“列表有更新”的提示照常起作用。
 */
export function useCarriedRevision(page: number) {
  const [carried, setCarried] = useState<{ page: number; revision: number } | null>(null);
  return {
    revision: carried !== null && carried.page === page ? carried.revision : null,
    carry: (target: number, revision: number) => setCarried({ page: target, revision }),
    /** 读取失败时调用：是“集合变了”就丢掉版本并返回 true。 */
    dropIfChanged: (error: unknown) => {
      const changed = isHttpFailure(failureOf(error), 409, "favorites_changed");
      if (changed && carried !== null) setCarried(null);
      return changed;
    },
  };
}

/** 翻页的链接在列表下方：把结果顶部滚进视野并聚焦区块标题，接着 Tab 就是第一行（与专辑列表、历史页相同）。 */
export function useResultsTop() {
  const headingRef = useRef<HTMLHeadingElement>(null);
  const showTop = () => {
    const heading = headingRef.current;
    if (heading === null) return;
    heading.scrollIntoView({ block: "start" });
    heading.focus({ preventScroll: true });
  };
  return { headingRef, showTop };
}

export function UpdatedHint({ onRefresh }: { onRefresh: () => void }) {
  return (
    <p className={styles.updated}>
      列表有更新。{" "}
      <Button onClick={onRefresh}>
        刷新
      </Button>
    </p>
  );
}
