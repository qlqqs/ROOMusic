import { Link } from "@tanstack/react-router";
import { Disc3, Feather, MicVocal, Music2, type LucideIcon } from "lucide-react";
import type { ReactNode } from "react";
import { formatCount } from "../../../shared/lib/number";
import { QueryContent, Skeleton } from "../../../shared/ui";
import type { CatalogStatsDTO } from "../api/decoders";
import { useCatalogStats } from "../hooks/use-catalog-stats";
import styles from "./library-stats.module.css";

type StatCard = { key: keyof CatalogStatsDTO; label: string; icon: LucideIcon; link: (content: ReactNode) => ReactNode };

const cards: readonly StatCard[] = [
  { key: "albums", label: "专辑", icon: Disc3, link: (content) => <Link to="/albums" className={styles.card}>{content}</Link> },
  { key: "tracks", label: "曲目", icon: Music2, link: (content) => <div className={styles.card}>{content}</div> },
  { key: "artists", label: "艺术家", icon: MicVocal, link: (content) => <Link to="/artists" className={styles.card}>{content}</Link> },
  { key: "composers", label: "作曲家", icon: Feather, link: (content) => <Link to="/artists" search={{ role: "composer" }} className={styles.card}>{content}</Link> },
];

/**
 * 概览顶部的资料库统计：专辑、曲目、艺术家、作曲家各一张卡（图标、数字、名称），除曲目外整张卡链接到对应列表。
 * 艺人数据还在整理时艺术家与作曲家显示“—”且不链接。资料库里还没有专辑时只说一句话。
 */
export function LibraryStats({ isAdmin }: { isAdmin: boolean }) {
  const stats = useCatalogStats();
  return (
    <section aria-label="资料库" className={styles.section}>
      <QueryContent query={stats} loading={<Skeleton label="正在加载资料库统计" lines={2} />}>
        {(data) =>
          data.albums === 0 ? (
            <p>{isAdmin ? "资料库里还没有专辑。添加音乐目录并扫描后就会出现。" : "资料库里还没有专辑。"}</p>
          ) : (
            <ul role="list" className={styles.cards}>
              {cards.map(({ key, label, icon: Icon, link }) => {
                const value = data[key];
                const content = (
                  <>
                    <Icon size={20} aria-hidden="true" className={styles.icon} />
                    <span className={styles.value}>{value === null ? "—" : formatCount(value)}</span>
                    <span className={styles.label}>{label}</span>
                  </>
                );
                return <li key={key}>{value === null ? <div className={styles.card}>{content}</div> : link(content)}</li>;
              })}
            </ul>
          )
        }
      </QueryContent>
    </section>
  );
}
