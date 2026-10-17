import { Link } from "@tanstack/react-router";
import { albumArtistOf } from "../../catalog";
import type { IssueListItemDTO } from "../api/decoders";
import { issueCountsText } from "../issue-text";
import styles from "./issue-list.module.css";

type IssueListProps = {
  items: readonly IssueListItemDTO[];
  /** 看的是待处理还是已核对：每行的数目取对应的一组。 */
  status: "open" | "reviewed";
  /** 选中的版本（地址里的 release）；它的链接带 aria-current。 */
  selectedId: string | undefined;
};

/**
 * 问题列表：每行一个版本，是链接（选中写进地址，前进、后退与分享都成立），当前项带 aria-current，与版本导航相同。
 * 一行写专辑名（最多 3 行，完整文字仍在可访问名称里）、版本说明与艺人，以及这一组的数目；链接只改 release，筛选与页码保留。
 */
export function IssueList({ items, status, selectedId }: IssueListProps) {
  return (
    <ul className={styles.list}>
      {items.map((item) => {
        const counts = issueCountsText(status === "reviewed" ? item.reviewed : item.open);
        const selected = item.release.id === selectedId;
        return (
          <li key={item.release.id} className={styles.item}>
            <Link
              to="/issues"
              search={(previous) => ({ ...previous, release: item.release.id })}
              aria-current={selected ? "page" : undefined}
              className={styles.link}
              data-selected={selected ? "" : undefined}
            >
              {/* 各段之间的空格让可访问名称读起来是“标题 版本说明 · 艺人 数目”，而不是连成一串。 */}
              <span className={styles.title}>{item.release.title}</span>{" "}
              <span className={styles.meta}>{[item.edition, albumArtistOf(item.release)].filter((part) => part !== null).join(" · ")}</span>{" "}
              <span className={styles.counts}>{counts === "" ? "已没有待处理的问题" : counts}</span>
            </Link>
          </li>
        );
      })}
    </ul>
  );
}
