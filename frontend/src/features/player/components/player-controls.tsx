import { Pause, Play, SkipBack, SkipForward, Volume2, VolumeX } from "lucide-react";
import { formatDuration } from "../../../shared/lib/duration";
import { Button, MenuButton } from "../../../shared/ui";
import { isPlaybackActive } from "../engine/player-engine";
import { playModes } from "../engine/queue";
import { usePlayer, usePlayerEngine, usePlayerProgress } from "../player-provider";
import { modeLabels } from "./player-labels";
import styles from "./player-controls.module.css";

export function TransportControls({ compact = false }: { compact?: boolean }) {
  const { engine, snapshot } = usePlayer();
  // 与 togglePlayback 用同一判定：加载中按下去是暂停（放弃这次加载），按钮就写“暂停”。
  const active = isPlaybackActive(snapshot.status);
  return <div className={`${styles.transport} ${compact ? styles.compact : ""}`}>
    <span className={compact ? styles.desktop : undefined}><Button aria-label="上一首" icon={<SkipBack size={18} />} disabled={snapshot.current === null} onClick={() => engine.previous()} /></span>
    <span className={styles.play}><Button aria-label={active ? "暂停" : "播放"} icon={active ? <Pause size={18} /> : <Play size={18} />} disabled={snapshot.current === null} onClick={() => engine.togglePlayback()} /></span>
    <Button aria-label="下一首" icon={<SkipForward size={18} />} disabled={!snapshot.hasNext} onClick={() => engine.next()} />
  </div>;
}
export function SeekControl() {
  const engine = usePlayerEngine();
  const progress = usePlayerProgress();
  const text = `${formatDuration(progress.positionSeconds)} / ${(progress.durationSeconds === null ? "—:—" : formatDuration(progress.durationSeconds))}`;
  return <div className={styles.seek}>
    <input type="range" aria-label="播放进度" aria-valuetext={text} min={0} max={progress.durationSeconds ?? 0} step={0.1} value={progress.positionSeconds} disabled={!progress.seekable || progress.durationSeconds === null} onChange={(event) => engine.seek(Number(event.target.value))} />
    <span>{text}</span>
  </div>;
}
export function ModeControl() {
  const { engine, snapshot } = usePlayer();
  return <MenuButton label={`播放模式：${modeLabels[snapshot.queue.mode]}`} actions={playModes.map((mode) => ({ id: mode, label: modeLabels[mode], onSelect: () => engine.setMode(mode) }))} />;
}
export function VolumeControl() {
  const { engine, snapshot } = usePlayer();
  return <div className={styles.volume}>
    <Button aria-label={snapshot.muted ? "取消静音" : "静音"} aria-pressed={snapshot.muted} icon={snapshot.muted ? <VolumeX size={18} /> : <Volume2 size={18} />} onClick={() => engine.setMuted(!snapshot.muted)} />
    <input type="range" aria-label="播放器音量" aria-valuetext={`${Math.round(snapshot.volume * 100)}%`} min={0} max={1} step={0.01} value={snapshot.volume} onChange={(event) => engine.setVolume(Number(event.target.value))} />
  </div>;
}
export function PlayerControls() {
  return <div className={styles.full}><TransportControls /><SeekControl /><div className={styles.options}><ModeControl /><VolumeControl /></div></div>;
}
