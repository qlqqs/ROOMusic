import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { useTooltip } from "../../../shared/ui";
import { ArtistNames } from "../../catalog";
import type { PlayerTrack } from "../engine/queue";
import { useFullscreenPlayer } from "../hooks/use-fullscreen-player";
import { DeviceButton } from "./device-picker";
import { usePlayerProgressSelector, usePlayerSelector } from "../player-provider";
import { PlayerArtwork } from "./player-artwork";
import { TransportControls, VolumeControl } from "./player-controls";
import { PlayerFaultInline } from "./player-feedback";
import { statusLabels } from "./player-labels";
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
  const tip = useTooltip("打开全屏播放器");
  return <>
    <button type="button" className={styles.cover} aria-label="打开全屏播放器" {...tip.triggerProps} onClick={(event) => openPlayer(event.currentTarget)}>
      <PlayerArtwork track={track} />
    </button>
    {tip.tooltip}
  </>;
}

/** 加载与缓冲时才写出状态；正常播放、暂停不占位置，出错的原因在标题下（PlayerFaultInline）。 */
function TransientStatus() {
  const status = usePlayerSelector((snapshot) => snapshot.status);
  if (status !== "loading" && status !== "buffering") return null;
  return <span className={`${styles.state} ${styles.desktop}`}>{statusLabels[status]}</span>;
}

/** 窄屏迷你栏底边的进度线：只是示意（对读屏隐藏），拖动与键盘在全屏播放器的进度条上。只动 transform。 */
function ProgressLine() {
  const ratio = usePlayerProgressSelector((progress) => (progress.durationSeconds === null || progress.durationSeconds <= 0 ? 0 : Math.min(1, progress.positionSeconds / progress.durationSeconds)));
  return <span className={styles.progressLine} aria-hidden="true"><span style={{ transform: `scaleX(${ratio})` }} /></span>;
}

/**
 * 底部播放栏。宽屏三段：左边曲目（封面、标题与信号路径圆点、艺人 — 专辑、心形），中间随机、上一首、播放、下一首、循环与两端写时间的进度，
 * 右边固定顺序：（加载与缓冲时的状态）队列、分隔线、设备、音量。窄屏是浮起的迷你栏：封面、标题与圆点、（控制别的设备时的）设备、播放、下一首、队列，
 * 底边一条进度线。
 */
export function PlayerBar({ renderCurrentTrackActions, renderQueueActions }: PlayerBarProps = {}) {
  const track = usePlayerSelector((snapshot) => snapshot.current?.track);
  // 无当前曲目时没有封面可打开全屏，仍须能选择其他在线设备或接手离线设备的队列。
  const showDevice = usePlayerSelector((snapshot) => (!snapshot.isLocal && snapshot.device !== null)
    || (snapshot.current === null && snapshot.devices.some((row) => !row.isLocal && (row.device.online || (row.zone?.current ?? null) !== null))));
  return <section aria-label="播放器" className={styles.bar} data-device-visible={showDevice ? "" : undefined}>
    <div className={styles.identity}>
      {track === undefined ? <div className={styles.text}><PlayerFaultInline /></div> : <><ExpandArtworkButton track={track} /><div className={styles.text}>
        <div className={styles.titleRow}><p className={styles.title} title={track.title}>{track.title}</p><SignalPathButton /></div>
        <PlayerFaultInline />
        <p className={styles.subtitle}><ArtistNames text={track.artist} refs={track.artistRefs ?? noRefs} /> — <Link to="/albums/$groupId" params={{ groupId: track.groupId }} search={{ release: track.releaseId }}>{track.albumTitle}</Link></p>
      </div>{renderCurrentTrackActions === undefined ? null : <div className={styles.trackActions}>{renderCurrentTrackActions(track)}</div>}</>}
    </div>
    <div className={styles.center}>
      <TransportControls size="compact" modes />
      <div className={styles.desktop}><WaveformSeek layout="ends" /></div>
    </div>
    <div className={styles.end}>
      <TransientStatus />
      <span className={styles.queue}><QueueButton renderQueueActions={renderQueueActions} /></span>
      <span className={`${styles.separator} ${styles.desktop}`} aria-hidden="true" />
      <DeviceButton className={styles.device} />
      <div className={`${styles.volume} ${styles.desktop}`}><VolumeControl /></div>
    </div>
    {track === undefined ? null : <ProgressLine />}
  </section>;
}
