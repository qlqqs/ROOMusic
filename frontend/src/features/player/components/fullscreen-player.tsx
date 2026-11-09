import { Link, useRouterState } from "@tanstack/react-router";
import { ChevronDown, Disc3 } from "lucide-react";
import { useCallback, useEffect, useId, useRef, useState, useSyncExternalStore, type CSSProperties, type ReactNode } from "react";
import { Button, ModalDialog, useTooltip } from "../../../shared/ui";
import { ArtistNames, artworkPath } from "../../catalog";
import { isPlaybackActive } from "../zones/controller";
import type { PlayerTrack } from "../engine/queue";
import { fullscreenOrigin, useFullscreenPlayer } from "../hooks/use-fullscreen-player";
import { usePlayerSelector } from "../player-provider";
import { LyricsPanel } from "./lyrics-panel";
import { PlayerArtwork } from "./player-artwork";
import { ModeControl, TransportControls, VolumeControl } from "./player-controls";
import { ControlTargetLine, DeviceButton } from "./device-picker";
import { PlayerFaultInline } from "./player-feedback";
import { QueueButton } from "./queue-drawer";
import { SignalPathButton } from "./signal-path";
import { WaveformSeek } from "./waveform-seek";
import styles from "./fullscreen-player.module.css";

type FullscreenPlayerProps = {
  /** 当前曲目的动作（例如收藏的心形），放在标题旁；由 app 组合。 */
  renderCurrentTrackActions?: ((track: PlayerTrack) => ReactNode) | undefined;
  /** 队列标题旁的动作（例如“存为歌单”），原样交给队列抽屉。 */
  renderQueueActions?: ((tracks: readonly PlayerTrack[]) => ReactNode) | undefined;
};

const NARROW = "(max-width: 767px)";
const REDUCED_MOTION = "(prefers-reduced-motion: reduce)";
const CLOSE_MS = 180;
const noRefs: readonly never[] = [];

function useMediaQuery(query: string): boolean {
  const subscribe = useCallback((onChange: () => void) => {
    const list = window.matchMedia(query);
    list.addEventListener("change", onChange);
    return () => list.removeEventListener("change", onChange);
  }, [query]);
  return useSyncExternalStore(subscribe, () => window.matchMedia(query).matches);
}

/**
 * 全屏播放器：点播放栏封面打开，向下箭头、Esc、浏览器返回收起（开合记在历史记录里）。
 * 桌面两栏（唱片 | 标题与歌词），窄屏单栏，点唱片与歌词切换；底栏是收起、信号路径、传输控制、进度、播放模式、队列与音量。
 */
export function FullscreenPlayer({ renderCurrentTrackActions, renderQueueActions }: FullscreenPlayerProps) {
  const { open, closePlayer } = useFullscreenPlayer();
  const track = usePlayerSelector((snapshot) => snapshot.current?.track ?? null);
  const pathname = useRouterState({ select: (state) => state.location.pathname });
  const titleId = useId();
  const collapseRef = useRef<HTMLButtonElement>(null);

  // 收起时先播放 180ms 的缩回动画再真正关闭；点专辑、艺人离开当前页面时直接关闭，焦点交给新页面。
  const [shown, setShown] = useState(false);
  const [closing, setClosing] = useState(false);
  const [previousOpen, setPreviousOpen] = useState(false);
  const [openedAt, setOpenedAt] = useState(pathname);
  if (open !== previousOpen) {
    setPreviousOpen(open);
    if (open) {
      setShown(true);
      setClosing(false);
      setOpenedAt(pathname);
    } else if (shown) {
      if (pathname !== openedAt || window.matchMedia(REDUCED_MOTION).matches) setShown(false);
      else setClosing(true);
    }
  }
  useEffect(() => {
    if (!closing) return;
    const timer = window.setTimeout(() => {
      setShown(false);
      setClosing(false);
    }, CLOSE_MS);
    return () => window.clearTimeout(timer);
  }, [closing]);

  // 队列被清空：没有可显示的曲目，收起。
  useEffect(() => {
    if (open && track === null) closePlayer();
  }, [open, track, closePlayer]);

  const origin = fullscreenOrigin();
  const originStyle = origin === null ? undefined : ({ "--origin-x": `${origin.x}px`, "--origin-y": `${origin.y}px` } as CSSProperties);
  return <ModalDialog open={shown} titleId={titleId} onRequestClose={closePlayer} initialFocus={collapseRef} className={styles.dialog}>
    <div className={styles.root} style={originStyle} data-closing={closing ? "" : undefined}>
      {track === null ? null : <>
        <Backdrop track={track} />
        <div className={styles.layout}>
          <span className={styles.collapse}><Button ref={collapseRef} variant="icon" icon={<ChevronDown size={22} />} label="收起全屏播放器" onClick={closePlayer} /></span>
          <Stage track={track} titleId={titleId} renderCurrentTrackActions={renderCurrentTrackActions} />
          <div className={styles.dock}>
            <div className={styles.signal}><SignalPathButton /></div>
            <div className={styles.transport}><TransportControls size="large" /></div>
            <div className={styles.seek}><WaveformSeek layout="ends" /></div>
            <div className={styles.options}><ModeControl /><DeviceButton /><QueueButton renderQueueActions={renderQueueActions} /><div className={styles.volume}><VolumeControl /></div></div>
          </div>
        </div>
      </>}
    </div>
  </ModalDialog>;
}

/** 背景：封面放大、模糊、压暗；没有封面时是深灰底色。 */
function Backdrop({ track }: { track: PlayerTrack }) {
  const [failedId, setFailedId] = useState<string | null>(null);
  if (track.artworkId === null || failedId === track.artworkId) return null;
  return <div className={styles.backdrop} aria-hidden="true"><img src={artworkPath(track.artworkId)} alt="" onError={() => setFailedId(track.artworkId)} /></div>;
}

function Stage({ track, titleId, renderCurrentTrackActions }: { track: PlayerTrack; titleId: string } & Pick<FullscreenPlayerProps, "renderCurrentTrackActions">) {
  const narrow = useMediaQuery(NARROW);
  const [view, setView] = useState<"deck" | "lyrics">("deck");
  const deckTip = useTooltip("显示歌词");
  const lyrics = <div className={styles.lyrics}><LyricsPanel trackId={track.trackId} /></div>;
  const info = <div className={styles.info}>
    <h2 id={titleId} className={styles.title} title={track.title}>{track.title}</h2>
    <p className={styles.meta}>
      <Link to="/albums/$groupId" params={{ groupId: track.groupId }} search={{ release: track.releaseId }} title={track.albumTitle}>{track.albumTitle}</Link>
      <span aria-hidden="true"> · </span>
      <ArtistNames text={track.artist} refs={track.artistRefs ?? noRefs} />
    </p>
    <PlayerFaultInline showStatus className={styles.status} />
    <ControlTargetLine className={styles.narrowOnly} />
    {renderCurrentTrackActions === undefined ? null : <div className={styles.trackActions}>{renderCurrentTrackActions(track)}</div>}
  </div>;
  // 宽屏：唱片在左，标题与歌词在右；窄屏：唱片与歌词占同一块，点唱片看歌词，歌词右上角的图标回到唱片。
  if (!narrow) return <div className={styles.stage}><div className={styles.deckCell}><VinylDeck track={track} /></div>{info}{lyrics}</div>;
  return <div className={styles.stage}>
    <div className={styles.view}>{view === "deck"
      ? <><button type="button" className={styles.deckButton} aria-label="显示歌词" {...deckTip.triggerProps} onClick={() => setView("lyrics")}><VinylDeck track={track} /></button>{deckTip.tooltip}</>
      : <>{lyrics}<span className={styles.showDeck}><Button variant="icon" icon={<Disc3 size={18} />} label="显示唱片" onClick={() => setView("deck")} /></span></>}</div>
    {info}
  </div>;
}

/** 黑胶唱片与唱臂：播放时唱片转、唱臂落下；暂停时唱片停在当前角度、唱臂抬起。 */
function VinylDeck({ track }: { track: PlayerTrack }) {
  const playing = usePlayerSelector((snapshot) => snapshot.status === "playing");
  const active = usePlayerSelector((snapshot) => isPlaybackActive(snapshot.status));
  return <div className={styles.deck} data-playing={playing ? "" : undefined} data-active={active ? "" : undefined}>
    <div className={styles.disc}><div className={styles.label}><PlayerArtwork track={track} /></div></div>
    <svg className={styles.arm} viewBox="0 0 100 200" aria-hidden="true">
      <circle cx="75" cy="18" r="13" className={styles.armBase} />
      <polyline points="75,18 62,150 40,178" className={styles.armRod} />
      <line x1="44" y1="172" x2="33" y2="192" className={styles.armHead} />
      <circle cx="75" cy="18" r="5" className={styles.armPivot} />
    </svg>
  </div>;
}
