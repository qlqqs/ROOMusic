import { Pause, Play, Repeat, Repeat1, Shuffle, SkipBack, SkipForward, Volume2, VolumeX } from "lucide-react";
import type { ReactNode } from "react";
import { formatDuration } from "../../../shared/lib/duration";
import { Button } from "../../../shared/ui";
import type { PlayMode } from "../engine/queue";
import { usePlayer, usePlayerEngine, usePlayerProgress } from "../player-provider";
import { repeatLabels } from "./player-labels";
import styles from "./player-controls.module.css";

type TransportProps = {
  /** compact 是播放栏（窄屏只留播放与下一首），large 是全屏播放器（播放键更大）。 */
  size?: "compact" | "default" | "large";
  /** 两侧加随机与循环按钮：随机、上一首、播放、下一首、循环一排。 */
  modes?: boolean;
};

/** 上一首、播放暂停、下一首；modes 时两侧是随机与循环。 */
export function TransportControls({ size = "default", modes = false }: TransportProps) {
  const compact = size === "compact";
  const { engine, snapshot } = usePlayer();
  // 与 togglePlayback 用同一判定：播放区的意图是播放时按下去是暂停，按钮就写“暂停”。
  const active = snapshot.zone?.intent === "play";
  const playSize = size === "large" ? 26 : 18;
  return <div className={`${styles.transport} ${compact ? styles.compact : ""} ${size === "large" ? styles.large : ""}`}>
    {modes ? <span className={compact ? styles.desktop : undefined}><ShuffleButton /></span> : null}
    <span className={compact ? styles.desktop : undefined}><Button variant="icon" label="上一首" icon={<SkipBack size={18} />} disabled={snapshot.current === null} onClick={() => engine.previous()} /></span>
    <span className={styles.play}><Button variant="icon" label={active ? "暂停" : "播放"} icon={active ? <Pause size={playSize} /> : <Play size={playSize} />} disabled={snapshot.current === null} onClick={() => engine.togglePlayback()} /></span>
    <Button variant="icon" label="下一首" icon={<SkipForward size={18} />} disabled={!snapshot.hasNext} onClick={() => engine.next()} />
    {modes ? <span className={compact ? styles.desktop : undefined}><RepeatButton /></span> : null}
  </div>;
}
/**
 * 播放进度：inline 是“进度条 + 位置 / 时长”，ends 是两端各一个时间（全屏）。给了 wave（波形画布）时，原生 range 透明地盖在波形上，
 * 点击、拖动、键盘与可访问名称都仍由它提供。
 */
export function SeekControl({ layout = "inline", wave }: { layout?: "inline" | "ends"; wave?: ReactNode }) {
  const engine = usePlayerEngine();
  const progress = usePlayerProgress();
  const position = formatDuration(progress.positionSeconds);
  const duration = progress.durationSeconds === null ? "—:—" : formatDuration(progress.durationSeconds);
  const text = `${position} / ${duration}`;
  const range = <input type="range" className={wave === undefined ? undefined : styles.overlay} aria-label="播放进度" aria-valuetext={text} min={0} max={progress.durationSeconds ?? 0} step={0.1} value={progress.positionSeconds} disabled={!progress.seekable || progress.durationSeconds === null} onChange={(event) => engine.seek(Number(event.target.value))} />;
  const input = wave === undefined ? range : <div className={styles.wave}>{wave}{range}</div>;
  if (layout === "ends") return <div className={styles.seek}><span aria-hidden="true">{position}</span>{input}<span aria-hidden="true">{duration}</span></div>;
  return <div className={styles.seek}>{input}<span>{text}</span></div>;
}
/**
 * 随机与循环两个按钮，底层仍是服务端的四选一播放模式，所以两者互斥：开随机即关循环，开循环即关随机。
 * 开启时图标用强调色，下方一个小圆点（不只靠颜色）。
 */
export function ShuffleButton() {
  const { engine, snapshot } = usePlayer();
  const on = snapshot.queue.mode === "shuffle";
  return <span className={styles.mode} data-on={on ? "" : undefined}><Button variant="icon" label="随机播放" aria-pressed={on} icon={<Shuffle size={16} />} onClick={() => engine.setMode(on ? "sequential" : "shuffle")} /></span>;
}

// 每按一次：关 → 列表循环 → 单曲循环 → 关；随机时按下进入列表循环。
const nextRepeat: Record<PlayMode, PlayMode> = { sequential: "repeat_all", repeat_all: "repeat_one", repeat_one: "sequential", shuffle: "repeat_all" };

/** 循环：读屏名写出当前状态（循环：关 / 列表循环 / 单曲循环），非“关”时按下态。 */
export function RepeatButton() {
  const { engine, snapshot } = usePlayer();
  const mode = snapshot.queue.mode;
  const on = mode === "repeat_all" || mode === "repeat_one";
  const Icon = mode === "repeat_one" ? Repeat1 : Repeat;
  return <span className={styles.mode} data-on={on ? "" : undefined}><Button variant="icon" label={repeatLabels[mode]} aria-pressed={on} icon={<Icon size={16} />} onClick={() => engine.setMode(nextRepeat[mode])} /></span>;
}
/** 音量属于正在控制的设备；那台设备不能调音量（iPhone / iPad 的网页）时禁用，悬停提示原因。 */
export function VolumeControl() {
  const { engine, snapshot } = usePlayer();
  const fixed = !snapshot.volumeControllable;
  return <div className={styles.volume} title={fixed ? "这台设备不能调音量" : undefined}>
    <Button variant="icon" label={snapshot.muted ? "取消静音" : "静音"} aria-pressed={snapshot.muted} icon={snapshot.muted ? <VolumeX size={16} /> : <Volume2 size={16} />} disabled={fixed} onClick={() => engine.setMuted(!snapshot.muted)} />
    <input type="range" aria-label="播放器音量" aria-valuetext={`${Math.round(snapshot.volume * 100)}%`} min={0} max={1} step={0.01} value={snapshot.volume} disabled={fixed} onChange={(event) => engine.setVolume(Number(event.target.value))} />
  </div>;
}
