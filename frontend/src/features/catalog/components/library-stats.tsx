import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { formatCount } from "../../../shared/lib/number";
import { QueryContent, Skeleton } from "../../../shared/ui";
import type { CatalogStatsDTO } from "../api/decoders";
import { useCatalogStats } from "../hooks/use-catalog-stats";
import styles from "./library-stats.module.css";

type Entry = { key: keyof CatalogStatsDTO; unit: string; link: ((className: string, content: ReactNode) => ReactNode) | null };

const entries: readonly Entry[] = [
  { key: "albums", unit: "张专辑", link: (className, content) => <Link to="/albums" className={className}>{content}</Link> },
  { key: "tracks", unit: "首曲目", link: null },
  { key: "artists", unit: "位艺人", link: (className, content) => <Link to="/artists" className={className}>{content}</Link> },
  { key: "composers", unit: "位作曲家", link: (className, content) => <Link to="/artists" search={{ role: "composer" }} className={className}>{content}</Link> },
];

/**
 * 概览页底的资料库账目：一行“214 张专辑 | 3,126 首曲目 | 186 位艺人 | 42 位作曲家”，除曲目外每项链接到对应列表。
 * 艺人数据还在整理时艺人与作曲家写“—”且不链接。资料库里还没有专辑时只说一句话。
 */
export function LibraryStats({ isAdmin }: { isAdmin: boolean }) {
  const stats = useCatalogStats();
  return (
    <section aria-label="资料库" className={styles.ledger}>
      <QueryContent query={stats} loading={<Skeleton label="正在加载资料库统计" lines={1} />}>
        {(data) =>
          data.albums === 0 ? (
            <p>{isAdmin ? "资料库里还没有专辑。添加音乐目录并扫描后就会出现。" : "资料库里还没有专辑。"}</p>
          ) : (
            <div className={styles.line}>
              <span className={styles.caption} aria-hidden="true">
                资料库
              </span>
              <ul role="list" className={styles.entries}>
                {entries.map(({ key, unit, link }) => {
                  const value = data[key];
                  const content = (
                    <>
                      <span className={styles.value}>{value === null ? "—" : formatCount(value)}</span>
                      <span className={styles.unit}>{unit}</span>
                    </>
                  );
                  return (
                    <li key={key} className={styles.entry}>
                      {value === null || link === null ? <span className={styles.item}>{content}</span> : link(`${styles.item} ${styles.link}`, content)}
                    </li>
                  );
                })}
              </ul>
            </div>
          )
        }
      </QueryContent>
    </section>
  );
}
