import { Link } from "@tanstack/react-router";
import { formatDateTime } from "../../../shared/lib/time";
import type { ClaimEntryDTO } from "../api/decoders";
import { holdsText, locationText } from "../claim-text";
import styles from "./claim-list.module.css";

type ClaimListProps = {
  items: readonly ClaimEntryDTO[];
  /** 选中的旧版本（地址里的 release）；它的链接带 aria-current。 */
  selectedId: string | undefined;
};

/**
 * 待认领列表：每行一个已不可见的旧版本，是链接（选中写进地址，前进、后退与分享都成立），当前项带 aria-current，与问题列表相同。
 * 一行写旧版本的标题（有修正时是修正后的名字，管理员认得出）、原来的专辑（和别的版本同在一张专辑里时）、最后所在的位置、最后一次看到的时间
 * 与留着的修改；链接只改 release，页码保留。各段之间的空格让可访问名称读起来是一句话，而不是连成一串。
 */
export function ClaimList({ items, selectedId }: ClaimListProps) {
  return (
    <ul className={styles.list}>
      {items.map((item) => {
        const selected = item.release.id === selectedId;
        return (
          <li key={item.release.id} className={styles.item}>
            <Link
              to="/claims"
              search={(previous) => ({ ...previous, release: item.release.id, target: undefined })}
              aria-current={selected ? "page" : undefined}
              className={styles.link}
            >
              <span className={styles.title}>{item.release.title}</span>{" "}
              {item.album === null ? null : (
                <>
                  <span className={styles.meta}>原来在《{item.album.title}》的专辑里</span>{" "}
                </>
              )}
              <span className={styles.meta}>{locationText(item.location)}</span>{" "}
              <span className={styles.meta}>最后一次看到 {formatDateTime(item.last_seen_at)}</span>{" "}
              <span className={styles.holds}>{holdsText(item.holds)}</span>
            </Link>
          </li>
        );
      })}
    </ul>
  );
}
