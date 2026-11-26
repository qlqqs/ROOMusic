import { Link } from "@tanstack/react-router";
import { ChevronLeft } from "lucide-react";
import type { ReactNode } from "react";
import { Button, Notice } from "../../../shared/ui";
import { ArtistMeta, artistRoleLabels, type ArtistSummaryDTO } from "../../catalog";
import type { ArtistBioDTO, ArtistImageDTO, ArtistLinksDTO, ArtistTrackRole } from "../api/decoders";
import type { ArtistMediaControls } from "../hooks/use-artist-media";
import { ArtistBio } from "./artist-bio";
import styles from "./artist-header.module.css";
import { ArtistPortrait } from "./artist-portrait";
import { UndoCapsule } from "./undo-capsule";

/** 页头列出的角色：作词、作曲等创作与幕后角色；专辑艺人、曲目艺人人人都有，不列。 */
export function headerRolesOf(trackRoles: readonly ArtistTrackRole[]): ArtistTrackRole[] {
  return trackRoles.filter((role) => role !== "track_artist");
}

/**
 * 艺人页的页头（经 Page 的 header 插槽，渲染唯一的 h1），与专辑页同一套：“‹ 艺人”返回、大圆章（有艺人图片时是图片，右下角是来源印章）、
 * 名字、角色（作词、作曲等，点了跳到下方这个角色的曲目）、概况（“12 张专辑 · 148 首曲目”）与操作区（播放、随机、心形、“更多”，由 app 组合），
 * 下方是简介。管理员另有图片、简介与对应的写入（media 不为 null），失败就近说明，标记不对后底部浮出撤回胶囊。
 * 容器窄（手机）时圆章与文字居中。
 */
export function ArtistHeader({
  artist,
  compactTitle,
  actions,
  trackRoles = [],
  image = null,
  bio = null,
  links = { musicbrainz: null, wikidata: null, judged_by_model: false, operation_id: null },
  media = null,
}: {
  artist: ArtistSummaryDTO;
  compactTitle: boolean;
  actions: ReactNode;
  /** 有曲目的曲目级角色（固定顺序）。 */
  trackRoles?: readonly ArtistTrackRole[];
  image?: ArtistImageDTO | null;
  bio?: ArtistBioDTO | null;
  links?: ArtistLinksDTO;
  media?: ArtistMediaControls | null;
}) {
  const roles = headerRolesOf(trackRoles);
  return (
    <header className={styles.container}>
      <Link to="/artists" className={styles.back} aria-label="返回艺人列表">
        <ChevronLeft size={16} aria-hidden="true" />
        艺人
      </Link>
      <div className={styles.hero}>
        <ArtistPortrait artist={artist} image={image} links={links} media={media} />
        <div className={styles.info}>
          <h1 className={styles.title} data-compact={compactTitle ? "" : undefined}>
            {artist.name}
          </h1>
          {roles.length === 0 ? null : (
            <ul className={styles.roles} aria-label="角色">
              {roles.map((role) => (
                <li key={role}>
                  <Link
                    to="/artists/$artistId"
                    params={{ artistId: artist.id }}
                    search={(previous) => ({ ...previous, track_role: role, tracks_page: undefined })}
                    hash="artist-tracks"
                    className={styles.role}
                  >
                    {artistRoleLabels[role]}
                  </Link>
                </li>
              ))}
            </ul>
          )}
          <ArtistMeta artist={artist} className={styles.meta} />
          <div className={styles.actions}>{actions}</div>
        </div>
      </div>
      {media?.error ? (
        <Notice tone="error" action={<Button onClick={media.dismissError}>知道了</Button>}>
          {media.error}
        </Notice>
      ) : null}
      <ArtistBio bio={bio} media={media} />
      {media?.pendingReject ? <UndoCapsule pending={media.pendingReject} onUndo={media.undoReject} /> : null}
    </header>
  );
}
