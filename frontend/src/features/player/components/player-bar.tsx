import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { ArtistNames } from "../../catalog";
import type { PlayerTrack } from "../engine/queue";
import { useFullscreenPlayer } from "../hooks/use-fullscreen-player";
import { usePlayer } from "../player-provider";
import { PlayerArtwork } from "./player-artwork";
import { ModeControl, TransportControls, VolumeControl } from "./player-controls";
import { PlayerFaultInline } from "./player-feedback";
import { sourceFormat, statusLabels } from "./player-labels";
import { QueueButton } from "./queue-drawer";
import { SignalPathButton } from "./signal-path";
import { WaveformSeek } from "./waveform-seek";
import styles from "./player-bar.module.css";

type PlayerBarProps = {
  /** 当前曲目旁的动作（例如收藏的心形）：由 app 组合，player 不认识这些功能；没有当前曲目时不调用。 */
  renderCurrentTrackActions?: ((track: PlayerTrack) => ReactNode) | undefined;
  /** 队列标题旁的动作（例如“存为歌单”），原样交给队列抽屉。 */
  renderQueueActions?: ((tracks: readonly PlayerTrack[]) => ReactNode) | undefined;
};

const noRefs: readonly never[] = [];

/** 封面按钮：放大成全屏播放器，全屏从封面的位置展开。 */
function ExpandArtworkButton({ track }: { track: PlayerTrack }) {
  const { openPlayer } = useFullscreenPlayer();
  return <button type="button" className={styles.cover} aria-label="打开全屏播放器" title="打开全屏播放器" onClick={(event) => openPlayer(event.currentTarget)}>
    <PlayerArtwork track={track} />
  </button>;
}

export function PlayerBar({ renderCurrentTrackActions, renderQueueActions }: PlayerBarProps = {}) {
  const { snapshot } = usePlayer();
  const track = snapshot.current?.track;
  return <section aria-label="播放器" className={styles.bar}>
    <div className={styles.identity}>
      {track === undefined ? <PlayerFaultInline /> : <><ExpandArtworkButton track={track} /><div className={styles.text}>
        <p className={styles.title} title={track.title}>{track.title}</p>
        <PlayerFaultInline />
        <p className={styles.subtitle}><ArtistNames text={track.artist} refs={track.artistRefs ?? noRefs} /> — <Link to="/albums/$groupId" params={{ groupId: track.groupId }} search={{ release: track.releaseId }}>{track.albumTitle}</Link></p>
      </div>{renderCurrentTrackActions === undefined ? null : <div className={styles.trackActions}>{renderCurrentTrackActions(track)}</div>}</>}
    </div>
    <div className={styles.center}><TransportControls size="compact" /><div className={styles.desktop}><WaveformSeek /></div></div>
    <div className={styles.end}>
      <div className={`${styles.desktop} ${styles.format}`}>{track === undefined ? null : <SignalPathButton />}<span className={styles.formatText}><span>{track === undefined ? "未知格式" : sourceFormat(track)}</span><span className={styles.state}>{statusLabels[snapshot.status]}</span></span></div>
      <div className={styles.desktop}><ModeControl /></div>
      <div className={styles.volume}><VolumeControl /></div>
      <QueueButton renderQueueActions={renderQueueActions} />
    </div>
  </section>;
}
