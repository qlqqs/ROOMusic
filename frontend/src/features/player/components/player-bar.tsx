import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { ArtistNames } from "../../catalog";
import type { PlayerTrack } from "../engine/queue";
import { usePlayer } from "../player-provider";
import { PlayerArtwork } from "./player-artwork";
import { ModeControl, SeekControl, TransportControls, VolumeControl } from "./player-controls";
import { failureLabels, sourceFormat, statusLabels } from "./player-labels";
import styles from "./player-bar.module.css";

type PlayerBarProps = {
  /** 当前曲目旁的动作（例如收藏的心形）：由 app 组合，player 不认识这些功能；没有当前曲目时不调用。 */
  renderCurrentTrackActions?: ((track: PlayerTrack) => ReactNode) | undefined;
};

const noRefs: readonly never[] = [];

export function PlayerBar({ renderCurrentTrackActions }: PlayerBarProps = {}) {
  const { snapshot } = usePlayer();
  const track = snapshot.current?.track;
  const currentFailure = snapshot.current === null ? undefined : snapshot.failures[snapshot.current.entryId];
  const faultText = snapshot.fault?.kind === "transient" ? "读取失败，进入正在播放页重试" : snapshot.fault?.kind === "busy" ? "服务器正忙于转换，进入正在播放页重试" : snapshot.fault?.kind === "autoplay_blocked" ? "请按播放" : currentFailure === undefined ? null : failureLabels[currentFailure];
  return <section aria-label="播放器" className={styles.bar}>
    <div className={styles.identity}>
      {track === undefined ? null : <><div className={styles.cover}><PlayerArtwork track={track} /></div><div className={styles.text}>
        <Link to="/now-playing" className={styles.title}>{track.title}</Link>
        {faultText === null ? null : <p className={styles.fault}>{faultText}</p>}
        <p className={styles.subtitle}><ArtistNames text={track.artist} refs={track.artistRefs ?? noRefs} /> — <Link to="/albums/$groupId" params={{ groupId: track.groupId }} search={{ release: track.releaseId }}>{track.albumTitle}</Link></p>
      </div>{renderCurrentTrackActions === undefined ? null : <div className={styles.trackActions}>{renderCurrentTrackActions(track)}</div>}</>}
    </div>
    <div className={styles.center}><TransportControls compact /><div className={styles.desktop}><SeekControl /></div></div>
    <div className={styles.end}>
      <div className={styles.desktop}><Link to="/now-playing" hash="signal-path">{track === undefined ? "未知格式" : sourceFormat(track)}</Link><span className={styles.state}>{statusLabels[snapshot.status]}</span></div>
      <div className={styles.desktop}><ModeControl /></div>
      <div className={styles.volume}><VolumeControl /></div>
      <Link to="/now-playing" hash="queue" className={styles.queue}>队列（{snapshot.queue.entries.length}）</Link>
    </div>
  </section>;
}
