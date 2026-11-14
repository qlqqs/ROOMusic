import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { ChevronDown, Disc3, MicVocal, MoreHorizontal, UserRound } from "lucide-react";
import { useEffect, useId, useRef, useState, type CSSProperties, type ReactNode, type RefObject } from "react";
import { useMediaQuery } from "../../../shared/lib/use-media-query";
import { Button, MenuButton, ModalDialog, useTooltip, type MenuAction } from "../../../shared/ui";
import { ArtistNames, artworkPath, useArtistLinksReady } from "../../catalog";
import { isPlaybackActive } from "../zones/controller";
import type { PlayerTrack } from "../engine/queue";
import { fullscreenOrigin, useFullscreenPlayer } from "../hooks/use-fullscreen-player";
import { useTrackLyrics } from "../hooks/use-track-lyrics";
import { usePlayerSelector } from "../player-provider";
import { LyricsPanel } from "./lyrics-panel";
import { PlayerArtwork } from "./player-artwork";
import { TransportControls, VolumeControl } from "./player-controls";
import { DeviceButton } from "./device-picker";
import { PlayerFaultInline } from "./player-feedback";
import { QueueButton } from "./queue-drawer";
import { SignalPathButton } from "./signal-path";
import { UpNextList } from "./up-next-list";
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

/**
 * 全屏播放器：点播放栏封面打开，左上角向下箭头、Esc、浏览器返回收起（开合记在历史记录里）。
 * 宽屏两栏等宽、整体居中：左边“唱机 + 控制台”（唱片下依次是标题与信号路径圆点、心形与更多、专辑 · 艺人、进度、传输键、音量），
 * 右边“歌词 / 接下来”切换；右上角是播放设备图标（队列从右栏的“接下来”看，不另放队列按钮）。
 * 窄屏单栏：顶部收起、“正在播放 · 专辑名”与更多；唱片视图与歌词视图切换（“歌词”按钮或点唱片），下面的进度、传输键与底部一排（设备 | 歌词、队列）位置不变。
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
        <Layout track={track} titleId={titleId} collapseRef={collapseRef} onCollapse={closePlayer} renderCurrentTrackActions={renderCurrentTrackActions} renderQueueActions={renderQueueActions} />
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

type LayoutProps = {
  track: PlayerTrack;
  titleId: string;
  collapseRef: RefObject<HTMLButtonElement | null>;
  onCollapse: () => void;
} & FullscreenPlayerProps;

function Layout({ track, titleId, collapseRef, onCollapse, renderCurrentTrackActions, renderQueueActions }: LayoutProps) {
  const narrow = useMediaQuery(NARROW);
  const collapse = <Button ref={collapseRef} variant="icon" icon={<ChevronDown size={22} />} label="收起全屏播放器" onClick={onCollapse} />;
  if (narrow) return <NarrowLayout track={track} titleId={titleId} collapse={collapse} queue={<QueueButton renderQueueActions={renderQueueActions} />} renderCurrentTrackActions={renderCurrentTrackActions} />;
  return <div className={styles.layout}>
    <div className={styles.topbar}><span className={styles.collapse}>{collapse}</span><DeviceButton /></div>
    <div className={styles.stage}>
      <div className={styles.left}>
        <VinylDeck track={track} />
        <div className={styles.console}>
          <div className={styles.info}>
            <div className={styles.titleRow}>
              <h2 id={titleId} className={styles.title} title={track.title}>{track.title}</h2>
              <span className={styles.signal}><SignalPathButton /></span>
              {renderCurrentTrackActions === undefined ? null : <div className={styles.trackActions}>{renderCurrentTrackActions(track)}</div>}
              <TrackMenu track={track} />
            </div>
            <TrackMeta track={track} />
            <PlayerFaultInline showStatus className={styles.status} />
          </div>
          <div className={styles.seek}><WaveformSeek layout="ends" /></div>
          <TransportControls size="large" modes />
          <div className={styles.volume}><VolumeControl /></div>
        </div>
      </div>
      <FullscreenPanel track={track} />
    </div>
  </div>;
}

/** “专辑 · 艺人”：都是链接（艺人数据还在整理时艺人是纯文字）。 */
function TrackMeta({ track }: { track: PlayerTrack }) {
  return <p className={styles.meta}>
    <Link to="/albums/$groupId" params={{ groupId: track.groupId }} search={{ release: track.releaseId }} title={track.albumTitle}>{track.albumTitle}</Link>
    <span aria-hidden="true"> · </span>
    <ArtistNames text={track.artist} refs={track.artistRefs ?? noRefs} />
  </p>;
}

/** 当前曲目的“更多”：转到专辑、转到各位艺人（与标题下的链接同一目标）。离开当前页面时全屏随之关闭，返回可回到全屏。 */
function TrackMenu({ track }: { track: PlayerTrack }) {
  const navigate = useNavigate();
  const linked = useArtistLinksReady();
  const actions: MenuAction[] = [
    { id: "album", label: "转到专辑", icon: <Disc3 size={16} />, onSelect: () => void navigate({ to: "/albums/$groupId", params: { groupId: track.groupId }, search: { release: track.releaseId } }) },
    ...(linked ? (track.artistRefs ?? noRefs).map((ref, index): MenuAction => ({ id: `artist-${index}`, label: `转到艺人：${ref.name}`, icon: <UserRound size={16} />, onSelect: () => void navigate({ to: "/artists/$artistId", params: { artistId: ref.artist_id } }) })) : []),
  ];
  return <MenuButton iconOnly label="更多操作" icon={<MoreHorizontal size={18} />} actions={actions} />;
}

/** 宽屏右栏：“歌词 / 接下来”。每次打开时，这首有歌词就先显示歌词，没有就显示接下来；用户选过之后保持所选。 */
function FullscreenPanel({ track }: { track: PlayerTrack }) {
  const lyrics = useTrackLyrics(track.trackId);
  const [choice, setChoice] = useState<"lyrics" | "upnext" | null>(null);
  const view = choice ?? (lyrics.data === null ? "upnext" : "lyrics");
  return <div className={styles.right}>
    <div className={styles.segmented} role="group" aria-label="右栏内容">
      <button type="button" aria-pressed={view === "lyrics"} onClick={() => setChoice("lyrics")}>歌词</button>
      <button type="button" aria-pressed={view === "upnext"} onClick={() => setChoice("upnext")}>接下来</button>
    </div>
    <div className={styles.panel}>{view === "lyrics" ? <LyricsPanel trackId={track.trackId} size="large" /> : <UpNextList />}</div>
  </div>;
}

type NarrowLayoutProps = { track: PlayerTrack; titleId: string; collapse: ReactNode; queue: ReactNode } & Pick<FullscreenPlayerProps, "renderCurrentTrackActions">;

/** 窄屏：上面的视图在唱片与歌词之间切换，下面的进度、传输键与底部一排（设备、歌词、队列）不随视图变化。 */
function NarrowLayout({ track, titleId, collapse, queue, renderCurrentTrackActions }: NarrowLayoutProps) {
  const [view, setView] = useState<"deck" | "lyrics">("deck");
  const deckTip = useTooltip("显示歌词");
  const actions = renderCurrentTrackActions === undefined ? null : <div className={styles.trackActions}>{renderCurrentTrackActions(track)}</div>;
  return <div className={styles.layout}>
    <div className={styles.topbar}>
      {collapse}
      <p className={styles.context}>正在播放<span className={styles.contextAlbum}>{track.albumTitle}</span></p>
      <TrackMenu track={track} />
    </div>
    {view === "deck"
      ? <div className={styles.view}>
        <div className={styles.deckCell}><button type="button" className={styles.deckButton} aria-label="显示歌词" {...deckTip.triggerProps} onClick={() => setView("lyrics")}><VinylDeck track={track} /></button>{deckTip.tooltip}</div>
        <div className={styles.info}>
          <div className={styles.titleRow}><h2 id={titleId} className={styles.title} title={track.title}>{track.title}</h2><span className={styles.signal}><SignalPathButton /></span>{actions}</div>
          <TrackMeta track={track} />
        </div>
      </div>
      : <div className={`${styles.view} ${styles.lyricsView}`}>
        <div className={styles.miniHead}>
          <span className={styles.miniArt}><PlayerArtwork track={track} /></span>
          <div className={styles.miniText}><h2 id={titleId} className={styles.miniTitle} title={track.title}>{track.title}</h2><p className={styles.meta}><ArtistNames text={track.artist} refs={track.artistRefs ?? noRefs} /></p></div>
          {actions}
        </div>
        <div className={styles.lyrics}><LyricsPanel trackId={track.trackId} /></div>
      </div>}
    <div className={styles.controls}>
      <PlayerFaultInline showStatus className={styles.status} />
      <div className={styles.seek}><WaveformSeek layout="ends" /></div>
      <TransportControls size="large" modes />
      <div className={styles.bottomRow}>
        <DeviceButton />
        <div className={styles.bottomActions}>
          <Button variant="icon" icon={<MicVocal size={20} />} label="歌词" aria-pressed={view === "lyrics"} onClick={() => setView(view === "lyrics" ? "deck" : "lyrics")} />
          {queue}
        </div>
      </div>
    </div>
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
