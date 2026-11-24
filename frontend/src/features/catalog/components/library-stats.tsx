import { Link } from "@tanstack/react-router";
import { ChevronRight } from "lucide-react";
import { useId, type ReactNode } from "react";
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
 * 概览右栏顶部的资料库：标题“资料库”在卡片外，卡片里 2×2 宫格，每格大号数字在上、单位在下；除曲目外整格链接到对应列表，单位后一个箭头。
 * 艺人数据还在整理时艺人与作曲家写“—”且不链接。资料库里还没有专辑时整块不出现：主栏的空曲库说明已经讲清楚了。
 * 外观与下面的“资料库状态”表同一种卡片。
 */
export function LibraryStats() {
  const stats = useCatalogStats();
  const headingId = useId();
  if (stats.data?.albums === 0) return null;
  return (
    <section aria-labelledby={headingId} className={styles.section}>
      <h2 id={headingId}>资料库</h2>
      <div className={styles.card}>
        <QueryContent query={stats} loading={<div className={styles.pending}><Skeleton label="正在加载资料库统计" lines={2} /></div>}>
          {(data) => (
            <ul role="list" className={styles.entries}>
              {entries.map(({ key, unit, link }) => {
                const value = data[key];
                const linked = value !== null && link !== null;
                const content = (
                  <>
                    <span className={styles.value}>{value === null ? "—" : formatCount(value)}</span>
                    <span className={styles.unit}>
                      {unit}
                      {linked ? <ChevronRight size={14} aria-hidden="true" className={styles.arrow} /> : null}
                    </span>
                  </>
                );
                return (
                  <li key={key} className={styles.entry}>
                    {linked ? link(`${styles.item} ${styles.link}`, content) : <span className={styles.item}>{content}</span>}
                  </li>
                );
              })}
            </ul>
          )}
        </QueryContent>
      </div>
    </section>
  );
}
