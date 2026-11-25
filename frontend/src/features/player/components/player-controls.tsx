import { ListOrdered, Pause, Play, Repeat, Repeat1, Shuffle, SkipBack, SkipForward, Volume1, Volume2, VolumeX, type LucideIcon } from "lucide-react";
import { useId, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { cx } from "../../../shared/lib/class-names";
import { formatDuration } from "../../../shared/lib/duration";
import { Button, ModalDialog, VisuallyHidden } from "../../../shared/ui";
import type { PlayMode } from "../engine/queue";
import { usePlayer, usePlayerEngine, usePlayerProgress } from "../player-provider";
import { playModeLabels } from "./player-labels";
import styles from "./player-controls.module.css";

type TransportProps = {
  /** compact 是播放栏（窄屏只留播放与下一首），large 是全屏播放器（播放键更大）。 */
  size?: "compact" | "default" | "large";
  /** 上一首之前的控件（信号路径、播放设备）；compact 窄屏时与上一首一起隐藏。 */
  leading?: ReactNode;
  /** 下一首之后的控件（播放模式、音量）；compact 窄屏时隐藏。 */
  trailing?: ReactNode;
};

/** 上一首、播放暂停、下一首；两侧插槽由调用方放信号路径、设备、播放模式与音量。 */
export function TransportControls({ size = "default", leading, trailing }: TransportProps) {
  const compact = size === "compact";
  const { engine, snapshot } = usePlayer();
  // 与 togglePlayback 用同一判定：播放区的意图是播放时按下去是暂停，按钮就写“暂停”。
  const active = snapshot.zone?.intent === "play";
  const playSize = size === "large" ? 26 : 18;
  const hideNarrow = compact ? styles.desktop : undefined;
  return <div className={cx(styles.transport, compact && styles.compact, size === "large" && styles.large)}>
    {leading === undefined ? null : <span className={cx(styles.slot, hideNarrow)}>{leading}</span>}
    <span className={hideNarrow}><Button variant="icon" label="上一首" icon={<SkipBack size={18} />} disabled={snapshot.current === null} onClick={() => engine.previous()} /></span>
    <span className={styles.play}><Button variant="icon" label={active ? "暂停" : "播放"} icon={active ? <Pause size={playSize} /> : <Play size={playSize} />} disabled={snapshot.current === null} onClick={() => engine.togglePlayback()} /></span>
    <Button variant="icon" label="下一首" icon={<SkipForward size={18} />} disabled={!snapshot.hasNext} onClick={() => engine.next()} />
    {trailing === undefined ? null : <span className={cx(styles.slot, hideNarrow)}>{trailing}</span>}
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
// 每按一次切到下一种：顺序 → 列表循环 → 单曲循环 → 随机 → 顺序。
const nextMode: Record<PlayMode, PlayMode> = { sequential: "repeat_all", repeat_all: "repeat_one", repeat_one: "shuffle", shuffle: "sequential" };
const modeIcons: Record<PlayMode, LucideIcon> = { sequential: ListOrdered, repeat_all: Repeat, repeat_one: Repeat1, shuffle: Shuffle };

/**
 * 播放模式：服务端的四选一模式合成一个按钮，按一次换下一种，图标随模式变化。读屏名与悬停提示写出当前模式（播放模式：顺序 / …），
 * 非顺序时是按下态：图标强调色加下方小圆点（不只靠颜色）。
 */
export function PlayModeButton() {
  const { engine, snapshot } = usePlayer();
  const mode = snapshot.queue.mode;
  const on = mode !== "sequential";
  const Icon = modeIcons[mode];
  return <span className={styles.mode} data-on={on ? "" : undefined}><Button variant="icon" label={playModeLabels[mode]} aria-pressed={on} icon={<Icon size={16} />} onClick={() => engine.setMode(nextMode[mode])} /></span>;
}

/**
 * 音量：只有一个喇叭图标（静音或音量为 0 时是静音图标），点开锚定在它上方的小弹层（图标在视口上半部时改在下方），
 * 里面是竖向音量滑块与静音开关。音量属于正在控制的设备；那台设备不能调音量（iPhone / iPad 的网页）时图标禁用，悬停提示原因。
 * 正在控制的播放区有组时调的是组音量（按比例调整组内各台）。
 */
export function VolumeButton() {
  const { snapshot } = usePlayer();
  const [anchor, setAnchor] = useState<DOMRect | null>(null);
  const fixed = !snapshot.volumeControllable;
  const silent = snapshot.muted || snapshot.volume === 0;
  const Icon = silent ? VolumeX : snapshot.volume < 0.5 ? Volume1 : Volume2;
  if (fixed && anchor !== null) setAnchor(null);
  // 局域网设备不能由软件调音量时，提示到设备上去调（M8 U3）。
  const deviceVolume = fixed && snapshot.device?.lan != null;
  return <span className={styles.volumeButton} title={fixed ? (deviceVolume ? "在设备上调音量" : "这台设备不能调音量") : undefined}>
    {deviceVolume ? <span className={styles.deviceVolume}>设备音量</span> : null}
    <Button variant="icon" label={snapshot.muted ? "音量（已静音）" : "音量"} aria-haspopup="dialog" aria-expanded={anchor !== null} icon={<Icon size={16} />} disabled={fixed} onClick={(event) => setAnchor(event.currentTarget.getBoundingClientRect())} />
    <VolumeDialog anchor={anchor} onClose={() => setAnchor(null)} />
  </span>;
}

/**
 * 桌面播放栏的音量：喇叭图标（按一下静音或取消静音，图标随音量变化）加一条常驻的横向音量条，拖动、点击或方向键直接调。
 * 那台设备不能由软件调音量时两者都禁用，悬停提示原因；局域网设备另写“设备音量”。
 */
export function InlineVolume() {
  const { engine, snapshot } = usePlayer();
  const fixed = !snapshot.volumeControllable;
  const silent = snapshot.muted || snapshot.volume === 0;
  const Icon = silent ? VolumeX : snapshot.volume < 0.5 ? Volume1 : Volume2;
  const deviceVolume = fixed && snapshot.device?.lan != null;
  const percent = `${Math.round(snapshot.volume * 100)}%`;
  return <span className={styles.inlineVolume} title={fixed ? (deviceVolume ? "在设备上调音量" : "这台设备不能调音量") : undefined}>
    {deviceVolume ? <span className={styles.deviceVolume}>设备音量</span> : null}
    <Button variant="icon" label={snapshot.muted ? "取消静音" : "静音"} aria-pressed={snapshot.muted} icon={<Icon size={16} />} disabled={fixed} onClick={() => engine.setMuted(!snapshot.muted)} />
    <input type="range" className={styles.inlineSlider} aria-label={snapshot.zone?.group == null ? "播放器音量" : "组音量"} aria-valuetext={snapshot.muted ? `已静音，${percent}` : percent} min={0} max={1} step={0.01} value={snapshot.volume} disabled={fixed} data-muted={snapshot.muted ? "" : undefined} onChange={(event) => engine.setVolume(Number(event.target.value))} />
  </span>;
}

function VolumeDialog({ anchor, onClose }: { anchor: DOMRect | null; onClose: () => void }) {
  const { engine, snapshot } = usePlayer();
  const titleId = useId();
  const sliderRef = useRef<HTMLInputElement>(null);
  const position = anchor === null ? undefined : ({ "--anchor-x": `${anchor.left + anchor.width / 2}px`, "--anchor-top": `${anchor.top}px`, "--anchor-bottom": `${anchor.bottom}px` } as CSSProperties);
  const below = anchor !== null && anchor.top < window.innerHeight / 2;
  const percent = `${Math.round(snapshot.volume * 100)}%`;
  return <ModalDialog open={anchor !== null} titleId={titleId} onRequestClose={onClose} closeOnBackdrop initialFocus={sliderRef} className={styles.volumeDialog}>
    <div className={styles.volumeCard} style={position} data-below={below ? "" : undefined}>
      <h2 id={titleId} className={styles.volumeValue}><VisuallyHidden>音量 </VisuallyHidden>{snapshot.muted ? "静音" : percent}</h2>
      <input ref={sliderRef} type="range" className={styles.volumeSlider} aria-label={snapshot.zone?.group == null ? "播放器音量" : "组音量"} aria-valuetext={percent} min={0} max={1} step={0.01} value={snapshot.volume} onChange={(event) => engine.setVolume(Number(event.target.value))} />
      <Button variant="icon" label={snapshot.muted ? "取消静音" : "静音"} aria-pressed={snapshot.muted} icon={snapshot.muted ? <VolumeX size={16} /> : <Volume2 size={16} />} onClick={() => engine.setMuted(!snapshot.muted)} />
    </div>
  </ModalDialog>;
}
