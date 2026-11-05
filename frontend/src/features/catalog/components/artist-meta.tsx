import { Fragment } from "react";
import { formatCount } from "../../../shared/lib/number";
import { artistRoleLabels, artistRoles, type ArtistSummaryDTO } from "../api/artist-summary";
import styles from "./artist-meta.module.css";

type ArtistMetaProps = {
  artist: Pick<ArtistSummaryDTO, "album_count" | "track_count" | "roles">;
  /** 是否带上参与的身份（“专辑艺人、作曲”）。 */
  withRoles?: boolean;
  className?: string | undefined;
};

/**
 * 艺人的概况：“1 张专辑 · 2 首曲目 · 专辑艺人、曲目艺人”。数量各是不可拆开的一块，身份名之间只在“、”后断开（keep-all），
 * 窄的时候只在“·”或“、”处换行，不会把“曲目艺人”断成“曲目艺 / 人”。为 0 的数量不写，什么都没有时不渲染。
 */
export function ArtistMeta({ artist, withRoles = false, className }: ArtistMetaProps) {
  const counts = [
    artist.album_count === 0 ? null : `${formatCount(artist.album_count)} 张专辑`,
    artist.track_count === 0 ? null : `${formatCount(artist.track_count)} 首曲目`,
  ].filter((part) => part !== null);
  // 发行艺人与专辑艺人都叫“专辑艺人”，只写一次（I6）。
  const roles = withRoles ? [...new Set(artistRoles.filter((role) => artist.roles.includes(role)).map((role) => artistRoleLabels[role]))] : [];
  const parts = [...counts, ...(roles.length === 0 ? [] : [roles.join("、")])];
  if (parts.length === 0) return null;
  return (
    <span className={className}>
      {parts.map((part, index) => (
        <Fragment key={part}>
          {index === 0 ? null : " · "}
          <span className={index < counts.length ? styles.part : styles.roles}>{part}</span>
        </Fragment>
      ))}
    </span>
  );
}
