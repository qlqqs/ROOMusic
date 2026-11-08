import { Heart, PencilLine } from "lucide-react";
import type { CSSProperties, ReactNode, RefObject } from "react";
import { formatCount } from "../../../shared/lib/number";
import { formatDate } from "../../../shared/lib/time";
import { Avatar, Button } from "../../../shared/ui";
import styles from "./collection-hero.module.css";

type CollectionHeroProps = {
  /** 页面的 h1：歌单名或“我喜欢的音乐”。 */
  title: string;
  cover: ReactNode;
  /** 渐变的起始色（封面取色、金色淡色等）；null 时用中性淡色。 */
  tint: string | null;
  /** 有它时标题后面有铅笔按钮“改名”。 */
  onRename?: (() => void) | undefined;
  renameBusy?: boolean;
  owner: string;
  /** 歌单的创建时间（RFC 3339）；我喜欢的音乐没有。 */
  createdAt?: string;
  /** “播放全部”与“···”。 */
  actions: ReactNode;
};

/**
 * 歌单与我喜欢的音乐的页头：左边封面，右边标题、头像与用户名（歌单加创建日期）、播放全部与“···”。
 * 背景从封面主色的淡色渐变到页面底色。容器窄于 560px 时封面缩小，仍是左封面右信息。
 */
export function CollectionHero({ title, cover, tint, onRename, renameBusy = false, owner, createdAt, actions }: CollectionHeroProps) {
  return (
    <header className={styles.container}>
      <div className={styles.hero} style={tint === null ? undefined : ({ "--hero-tint": tint } as CSSProperties)}>
        <div className={styles.cover}>{cover}</div>
        <div className={styles.info}>
          <div className={styles.titleRow}>
            <h1>{title}</h1>
            {onRename === undefined ? null : <Button variant="icon" icon={<PencilLine size={16} />} label="改名" busy={renameBusy} onClick={onRename} />}
          </div>
          <p className={styles.owner}>
            <Avatar name={owner} />
            <span className={styles.name}>{owner}</span>
            {createdAt === undefined ? null : <time dateTime={createdAt}>{formatDate(createdAt)} 创建</time>}
          </p>
          <div className={styles.actions}>{actions}</div>
        </div>
      </div>
    </header>
  );
}

/** 我喜欢的音乐的封面：金色淡底上一颗实心心形。装饰性的。 */
export function LikedCover() {
  return (
    <div className={styles.liked} aria-hidden="true">
      <Heart fill="currentColor" />
    </div>
  );
}

type SongsHeadingProps = {
  headingRef: RefObject<HTMLHeadingElement | null>;
  /** 曲目数；还没读到时只写“歌曲”。 */
  count: number | null;
  /** 旁边的灰色小字（歌单的总时长）；空串不显示。 */
  detail?: string;
};

/** 页头下方的“歌曲 N”：翻页与移除后程序把焦点放在这里，接着 Tab 就是列表上方的提示或第一行。 */
export function SongsHeading({ headingRef, count, detail = "" }: SongsHeadingProps) {
  return (
    <div className={styles.songs}>
      <h2 ref={headingRef} tabIndex={-1} className={styles.songsTitle}>
        歌曲{count === null ? null : <span className={styles.count}> {formatCount(count)}</span>}
      </h2>
      {detail === "" ? null : <span className={styles.songsDetail}>{detail}</span>}
    </div>
  );
}
