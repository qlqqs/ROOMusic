import { Link } from "@tanstack/react-router";
import { ChevronLeft } from "lucide-react";
import type { ReactNode } from "react";
import type { ArtistBioDTO, ArtistImageDTO } from "../api/decoders";
import { ArtistMeta, artistRoleLabels, artistRoles, type ArtistSummaryDTO } from "../../catalog";
import { ArtistBio } from "./artist-bio";
import styles from "./artist-header.module.css";
import { ArtistMonogram } from "./artist-monogram";

/** 参与的角色，按固定顺序；发行艺人与专辑艺人只写一次“专辑艺人”。 */
function roleLabelsOf(roles: readonly ArtistSummaryDTO["roles"][number][]): string[] {
  return [...new Set(artistRoles.filter((role) => roles.includes(role)).map((role) => artistRoleLabels[role]))];
}

/**
 * 艺人页的页头（经 Page 的 header 插槽，渲染唯一的 h1），与专辑页同一套：“‹ 艺人”返回、大圆章（有艺人图片时是图片，否则是名字缩写）、
 * 名字、角色标签、概况（“12 张专辑 · 148 首曲目”）与操作区（播放、随机、心形、“更多”，由 app 组合），下方是简介与署名（有时）。
 * 容器窄（手机）时圆章与文字居中。
 */
export function ArtistHeader({
  artist,
  compactTitle,
  actions,
  image = null,
  bio = null,
}: {
  artist: ArtistSummaryDTO;
  compactTitle: boolean;
  actions: ReactNode;
  image?: ArtistImageDTO | null;
  bio?: ArtistBioDTO | null;
}) {
  const roles = roleLabelsOf(artist.roles);
  return (
    <header className={styles.container}>
      <Link to="/artists" className={styles.back} aria-label="返回艺人列表">
        <ChevronLeft size={16} aria-hidden="true" />
        艺人
      </Link>
      <div className={styles.hero}>
        <ArtistMonogram id={artist.id} name={artist.name} image={image?.resource ?? artist.image} size="large" />
        <div className={styles.info}>
          <h1 className={styles.title} data-compact={compactTitle ? "" : undefined}>
            {artist.name}
          </h1>
          {roles.length === 0 ? null : (
            <ul className={styles.roles} aria-label="角色">
              {roles.map((label) => (
                <li key={label}>{label}</li>
              ))}
            </ul>
          )}
          <ArtistMeta artist={artist} className={styles.meta} />
          <div className={styles.actions}>{actions}</div>
        </div>
      </div>
      <ArtistBio bio={bio} image={image} />
    </header>
  );
}
