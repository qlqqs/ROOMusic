import { Link } from "@tanstack/react-router";
import { artistMetaText, artistRolesText } from "../../catalog";
import type { SearchArtistItemDTO } from "../api/decoders";
import { matchNote } from "../match-note";
import styles from "./artist-results.module.css";

/** 艺人结果：名字是去艺人页的链接，下面是概况；按别名找到时再写一行“别名：Teresa Teng”。 */
export function ArtistResults({ items }: { items: readonly SearchArtistItemDTO[] }) {
  return (
    <ul className={styles.list}>
      {items.map((item) => {
        const { artist } = item;
        const meta = [artistMetaText(artist), artistRolesText(artist.roles)].filter((part) => part !== "").join(" · ");
        const note = matchNote(item.match, [artist.name]);
        return (
          <li key={artist.id} className={styles.item}>
            <Link to="/artists/$artistId" params={{ artistId: artist.id }} className={styles.name}>
              {artist.name}
            </Link>
            {meta === "" ? null : <span className={styles.meta}>{meta}</span>}
            {note === null ? null : <span className={styles.note}>{note}</span>}
          </li>
        );
      })}
    </ul>
  );
}
