import { Fragment } from "react";
import { formatCount } from "../../../shared/lib/number";
import type { ArtistSummaryDTO } from "../api/artist-summary";
import styles from "./artist-meta.module.css";

type ArtistMetaProps = {
  artist: Pick<ArtistSummaryDTO, "album_count" | "track_count">;
  className?: string | undefined;
};

/**
 * 艺人的概况：“1 张专辑 · 2 首曲目”。每个数量是不可拆开的一块，窄的时候只在“·”处换行。为 0 的数量不写，什么都没有时不渲染。
 */
export function ArtistMeta({ artist, className }: ArtistMetaProps) {
  const parts = [
    artist.album_count === 0 ? null : `${formatCount(artist.album_count)} 张专辑`,
    artist.track_count === 0 ? null : `${formatCount(artist.track_count)} 首曲目`,
  ].filter((part) => part !== null);
  if (parts.length === 0) return null;
  return (
    <span className={className}>
      {parts.map((part, index) => (
        <Fragment key={part}>
          {index === 0 ? null : " · "}
          <span className={styles.part}>{part}</span>
        </Fragment>
      ))}
    </span>
  );
}
