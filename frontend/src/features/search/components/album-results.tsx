import { Link } from "@tanstack/react-router";
import { StatusTag } from "../../../shared/ui";
import { albumArtistOf, albumMeta, Cover } from "../../catalog";
import type { SearchAlbumItemDTO } from "../api/decoders";
import { matchNote } from "../match-note";
import styles from "./album-results.module.css";

/**
 * 专辑结果：与专辑列表相同的卡片（整张卡片是一个链接，封面是装饰），卡片显示代表版本；
 * 命中的是别的版本时，链接直接打开那个版本。命中的不是标题或艺人时，标题下方一行小字说明匹配到了什么。
 */
export function AlbumResults({ items }: { items: readonly SearchAlbumItemDTO[] }) {
  return (
    <ul className={styles.grid}>
      {items.map((item) => {
        const release = item.group.representative;
        const artist = albumArtistOf(release);
        const note = matchNote(item.match, [release.title, artist]);
        const matched = item.matched_release_id === release.id ? undefined : item.matched_release_id;
        return (
          <li key={item.group.id} className={styles.item}>
            <Link to="/albums/$groupId" params={{ groupId: item.group.id }} search={{ release: matched }} className={styles.card}>
              <Cover artwork={release.artwork} title={release.title} variant="card" />
              <span className={styles.title}>{release.title}</span> <span className={styles.artist}>{artist}</span>{" "}
              <span className={styles.meta}>{albumMeta(release)}</span>
              {note === null ? null : (
                <>
                  {" "}
                  <span className={styles.note}>{note}</span>
                </>
              )}
              {item.group.release_count > 1 ? (
                <span className={styles.tags}>
                  <StatusTag>{item.group.release_count} 个版本</StatusTag>
                </span>
              ) : null}
            </Link>
          </li>
        );
      })}
    </ul>
  );
}
