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
 * 概览右栏顶部的资料库卡片：标题“资料库”，下面四行“数字 + 单位”（数字右对齐、等宽），除曲目外每行链接到对应列表，行尾一个箭头。
 * 艺人数据还在整理时艺人与作曲家写“—”且不链接。资料库里还没有专辑时只说一句话。外观与下面的“资料库状态”表同一种卡片。
 */
export function LibraryStats({ isAdmin }: { isAdmin: boolean }) {
  const stats = useCatalogStats();
  const headingId = useId();
  return (
    <section aria-labelledby={headingId} className={styles.card}>
      <h2 id={headingId} className={styles.title}>
        资料库
      </h2>
      <QueryContent query={stats} loading={<div className={styles.pending}><Skeleton label="正在加载资料库统计" lines={2} /></div>}>
        {(data) =>
          data.albums === 0 ? (
            <p className={styles.empty}>{isAdmin ? "资料库里还没有专辑。添加音乐目录并扫描后就会出现。" : "资料库里还没有专辑。"}</p>
          ) : (
            <ul role="list" className={styles.entries}>
              {entries.map(({ key, unit, link }) => {
                const value = data[key];
                const linked = value !== null && link !== null;
                const content = (
                  <>
                    <span className={styles.value}>{value === null ? "—" : formatCount(value)}</span>
                    <span className={styles.unit}>{unit}</span>
                    <span className={styles.arrow} aria-hidden="true">{linked ? <ChevronRight size={14} /> : null}</span>
                  </>
                );
                return (
                  <li key={key} className={styles.entry}>
                    {linked ? link(`${styles.item} ${styles.link}`, content) : <span className={styles.item}>{content}</span>}
                  </li>
                );
              })}
            </ul>
          )
        }
      </QueryContent>
    </section>
  );
}
