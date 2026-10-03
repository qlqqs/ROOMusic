import { useId, type MouseEvent } from "react";
import { formatCount } from "../../../shared/lib/number";
import { StatusTag, VisuallyHidden } from "../../../shared/ui";
import type { ReleaseMediumDTO, ReleaseTrackDTO } from "../api/decoders";
import { creditsText, formatDuration, mediumHeading } from "../format";
import styles from "./release-media.module.css";

type ReleaseMediaProps = {
  media: readonly ReleaseMediumDTO[];
  /** 专辑艺人：曲目艺人与它相同时不重复显示。 */
  albumArtist: string;
};

function TrackRow({ track, albumArtist }: { track: ReleaseTrackDTO; albumArtist: string }) {
  const title = track.title.trim() === "" ? "（无标题）" : track.title;
  const artist = track.artist.trim();
  const credits = creditsText(track.credits);
  const isCue = track.source_kind === "cue_virtual" || track.cue !== null;
  return (
    <li className={styles.track}>
      <span className={styles.number}>{track.position >= 1 ? track.position : "—"}</span>
      <div className={styles.body}>
        <p className={styles.title}>
          <span>{title}</span>
          {isCue ? <StatusTag>CUE 分轨</StatusTag> : null}
        </p>
        {artist === "" || artist === albumArtist ? null : <p className={styles.secondary}>{artist}</p>}
        {credits === "" ? null : <p className={styles.secondary}>{credits}</p>}
      </div>
      <span className={styles.duration}>
        {track.duration_seconds === null ? (
          <>
            <span aria-hidden="true">{formatDuration(null)}</span>
            <VisuallyHidden>时长未知</VisuallyHidden>
          </>
        ) : (
          formatDuration(track.duration_seconds)
        )}
      </span>
    </li>
  );
}

// 跳到某一张介质：把标题滚到视野里并聚焦（读屏随之读出“第 N 张”），不改地址、不增加历史项。
function jumpTo(event: MouseEvent<HTMLAnchorElement>, headingId: string) {
  event.preventDefault();
  const heading = document.getElementById(headingId);
  if (heading === null) return;
  heading.scrollIntoView({ block: "start" });
  heading.focus({ preventScroll: true });
}

function TrackList({ tracks, albumArtist }: { tracks: readonly ReleaseTrackDTO[]; albumArtist: string }) {
  if (tracks.length === 0) return <p>这一张里没有可显示的曲目。</p>;
  // 去掉了列表样式的 ol 在 Safari 里会失去列表语义，所以显式写出 role。
  return (
    <ol className={styles.tracks} role="list">
      {tracks.map((track) => (
        <TrackRow key={track.id} track={track} albumArtist={albumArtist} />
      ))}
    </ol>
  );
}

/**
 * 介质逐段列出：一张时直接是曲目列表，多于一张时每张一个带标题的分段，顶部有“跳到第 N 张”的页内链接。
 * 每个分段用 content-visibility 跳过屏幕外的布局，万首曲目的版本也能流畅滚动；文字都在 DOM 里，页内查找照常可用。
 */
export function ReleaseMedia({ media, albumArtist }: ReleaseMediaProps) {
  const idPrefix = useId();
  if (media.length === 0) return <p>这个版本没有可显示的曲目。</p>;
  const [only] = media;
  if (only !== undefined && media.length === 1) return <TrackList tracks={only.tracks} albumArtist={albumArtist} />;

  const headingIdOf = (index: number) => `${idPrefix}-medium-${index}`;
  return (
    <div className={styles.media}>
      <nav aria-label="介质" className={styles.jump}>
        <span>跳到</span>
        <ul className={styles.jumpList}>
          {media.map((medium, index) => (
            <li key={medium.id}>
              <a href={`#${headingIdOf(index)}`} onClick={(event) => jumpTo(event, headingIdOf(index))}>
                {mediumHeading(medium, index)}
              </a>
            </li>
          ))}
        </ul>
      </nav>
      {media.map((medium, index) => (
        <section key={medium.id} className={styles.medium} aria-labelledby={headingIdOf(index)}>
          <h3 id={headingIdOf(index)} tabIndex={-1} className={styles.heading}>
            {mediumHeading(medium, index)}
            <span className={styles.count}>，{formatCount(medium.tracks.length)} 首</span>
          </h3>
          <TrackList tracks={medium.tracks} albumArtist={albumArtist} />
        </section>
      ))}
    </div>
  );
}
